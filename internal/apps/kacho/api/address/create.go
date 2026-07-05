// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ExternalAddrSpec — спецификация внешнего адреса.
type ExternalAddrSpec struct {
	Address      string
	ZoneID       string
	Requirements *AddrRequirements
}

// AddrRequirements — параметры внешнего IP (DDoS provider, SMTP capability).
type AddrRequirements struct {
	DdosProtectionProvider string
	OutgoingSmtpCapability string
}

// InternalAddrSpec — спецификация внутреннего адреса (v4/v6 одинаковый shape).
type InternalAddrSpec struct {
	Address  string
	SubnetID string
}

// CreateInput — параметры для CreateAddressUseCase.Execute.
//
// Композирует поля Address-запроса + четыре family-specific spec'а (External
// v4/v6, Internal v4/v6) — это не тривиальная обертка вокруг domain.Address.
// Spec'и нельзя смержить в domain.Address: там oneof выражен через указатели, и
// валидация семейств через nil-проверки плоского CreateInput получается чище.
type CreateInput struct {
	ProjectID          string
	Name               string
	Description        string
	Labels             map[string]string
	DeletionProtection bool
	// Для external IPv4 (если ExternalSpec != nil):
	ExternalSpec *ExternalAddrSpec
	// Для internal IPv4 (если InternalSpec != nil):
	InternalSpec *InternalAddrSpec
	// Для internal IPv6 (если InternalIpv6Spec != nil):
	InternalIpv6Spec *InternalAddrSpec
	// Для external IPv6 (если ExternalIpv6Spec != nil):
	ExternalIpv6Spec *ExternalAddrSpec
}

// CreateAddressUseCase инициирует создание Address (multi-family). Sync-
// проверки (project exists, subnet exists, name unique, explicit-IP-in-CIDR)
// выполняются ДО создания Operation — клиент получает fast-fail gRPC-status,
// а не «200 + операция, упавшая через секунду». Async-часть (`doCreate`)
// собирает domain.Address по family, Insert, затем inline IPAM allocation
// (AllocateExternalIP/AllocateInternalIP/AllocateInternalIPv6/AllocateExternalIPv6).
//
// pools может быть nil в test-setup'ах — тогда v4/v6-external allocate
// недоступен, IP в результате остается пустым (Allocate*IP возвращает
// Unavailable). v6-internal allocate не зависит от pools — он работает
// random-pick'ом внутри subnet.v6_cidr_blocks.
//
// worker открывает ОДНУ writer-TX и делает в ней Insert(Address) → Allocate*IP
// (через writer.Addresses().Set*Spec / AllocateIPFromFreelist /
// AllocateExternalIPv6) → Outbox.Emit Address.CREATED. Либо весь композит виден
// (Commit), либо ничего (Abort/crash) — окно orphan-address-without-allocated-IP
// закрыто, compensating delete-after-failure не нужен (Abort сам снимает Insert).
type CreateAddressUseCase struct {
	repo          Repo
	subnetReader  SubnetReader
	projectClient ProjectClient
	opsRepo       operations.Repo
	pools         PoolService // nil → external IPAM недоступна (test-only)
	registrar     fgaregister.Registrar
}

// WithRegistrar подключает синхронный owner-tuple registrar (Decision 2): после
// commit Address owner-tuple синхронно регистрируется в kacho-iam. Nil →
// sync-путь пропускается (только async drainer).
func (u *CreateAddressUseCase) WithRegistrar(r fgaregister.Registrar) *CreateAddressUseCase {
	u.registrar = r
	return u
}

// NewCreateAddressUseCase создает CreateAddressUseCase.
func NewCreateAddressUseCase(r Repo, subnetReader SubnetReader, projectClient ProjectClient, opsRepo operations.Repo, pools PoolService) *CreateAddressUseCase {
	return &CreateAddressUseCase{
		repo:          r,
		subnetReader:  subnetReader,
		projectClient: projectClient,
		opsRepo:       opsRepo,
		pools:         pools,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
func (u *CreateAddressUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	if in.InternalSpec != nil {
		if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, in.InternalSpec.SubnetID); err != nil {
			return nil, err
		}
	}
	if in.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if in.InternalIpv6Spec != nil {
		if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, in.InternalIpv6Spec.SubnetID); err != nil {
			return nil, err
		}
	}
	if in.ExternalSpec == nil && in.InternalSpec == nil && in.InternalIpv6Spec == nil && in.ExternalIpv6Spec == nil {
		return nil, status.Error(codes.InvalidArgument, "address_spec required")
	}

	// Domain self-validation: NameVPC / Description / Labels через newtypes —
	// use-case не зовет corevalidate.* напрямую, все проходит через
	// Address.Validate(). VPC Address: пустое name допустимо (разрешительная
	// политика валидации).
	addrForValidate := domain.Address{
		Name:        domain.RcNameVPC(in.Name),
		Description: domain.RcDescription(in.Description),
		Labels:      domain.LabelsFromMap(in.Labels),
	}
	if err := serviceerr.FromValidation(addrForValidate.Validate()); err != nil {
		return nil, err
	}

	// requirements.ddos_protection_provider — только из whitelist;
	// requirements.outgoing_smtp_capability — только пустое.
	if in.ExternalSpec != nil && in.ExternalSpec.Requirements != nil {
		if err := corevalidate.DdosProvider(
			"external_ipv4_address_spec.requirements.ddos_protection_provider",
			in.ExternalSpec.Requirements.DdosProtectionProvider,
		); err != nil {
			return nil, err
		}
		if err := corevalidate.SmtpCapability(
			"external_ipv4_address_spec.requirements.outgoing_smtp_capability",
			in.ExternalSpec.Requirements.OutgoingSmtpCapability,
		); err != nil {
			return nil, err
		}
	}

	// Sync project.Exists precheck убран как race-prone: между sync-проверкой и
	// async-частью project может быть удален peer-сервисом, и second-writer-wins
	// безусловно создавал бы ресурс. NotFound возвращается через
	// `operation.error` из async `doCreate`. Sync subnet/uniqueness-проверки
	// (через DB-state в той же сервис-БД) остаются — они race-free относительно
	// peer-сервисов.
	if in.InternalSpec != nil && in.InternalSpec.SubnetID != "" {
		if _, err := u.subnetReader.Get(ctx, in.InternalSpec.SubnetID); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", in.InternalSpec.SubnetID)
			}
			return nil, serviceerr.MapRepoErr(err)
		}
	}
	if in.InternalIpv6Spec != nil && in.InternalIpv6Spec.SubnetID != "" {
		if _, err := u.subnetReader.Get(ctx, in.InternalIpv6Spec.SubnetID); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", in.InternalIpv6Spec.SubnetID)
			}
			return nil, serviceerr.MapRepoErr(err)
		}
	}
	if in.Name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}
		existing, _, lerr := rd.Addresses().List(ctx, AddressFilter{ProjectID: in.ProjectID, Name: in.Name}, Pagination{})
		_ = rd.Close()
		if lerr != nil {
			return nil, serviceerr.MapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "Address with name %s already exists", in.Name)
		}
	}

	// Sync-проверка: explicit IP (`internal_ipv4_address_spec.address`) должен
	// принадлежать CIDR-блоку указанной subnet. Если IP вне CIDR — возвращаем
	// sync InvalidArgument: иначе адрес попадает в БД минуя любые FK, что
	// приводит к мусору в IPAM.
	if in.InternalSpec != nil && in.InternalSpec.SubnetID != "" && in.InternalSpec.Address != "" {
		if err := u.validateInternalIPInSubnet(ctx, in.InternalSpec.SubnetID, in.InternalSpec.Address); err != nil {
			return nil, err
		}
	}
	// Симметрично v4: explicit internal IPv6 тоже обязан лежать в одном из
	// v6_cidr_blocks указанной subnet — иначе мусорный IP попадал бы в БД.
	if in.InternalIpv6Spec != nil && in.InternalIpv6Spec.SubnetID != "" && in.InternalIpv6Spec.Address != "" {
		if err := u.validateInternalIPv6InSubnet(ctx, in.InternalIpv6Spec.SubnetID, in.InternalIpv6Spec.Address); err != nil {
			return nil, err
		}
	}

	addrID := ids.NewID(ids.PrefixAddress)
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create address %s", in.Name),
		&vpcv1.CreateAddressMetadata{AddressId: addrID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, addrID, in)
	})

	return &op, nil
}

// validateInternalIPInSubnet проверяет sync-ом, что explicit IP лежит в CIDR
// одной из v4_cidr_blocks указанной subnet. Если subnet не найден — пропуск
// (NotFound будет возвращен async через doCreate, как и для всех остальных FK).
// Любая другая ошибка чтения subnetReader → Internal: pass-through.
func (u *CreateAddressUseCase) validateInternalIPInSubnet(ctx context.Context, subnetID, address string) error {
	sub, err := u.subnetReader.Get(ctx, subnetID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			// Subnet 404 — отдаем async, не sync.
			return nil
		}
		return serviceerr.MapRepoErr(err)
	}
	if len(sub.V4CidrBlocks) == 0 {
		// CIDR-less subnet (v4_cidr_blocks не required на Subnet.Create): нельзя
		// ни валидировать explicit address, ни выделить internal IPv4 в такой
		// подсети.
		return status.Errorf(codes.FailedPrecondition, "subnet %s has no IPv4 CIDR", subnetID)
	}
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return serviceerr.InvalidArg(
			"internal_ipv4_address_spec.address",
			"address is not a valid IP",
		)
	}
	for _, raw := range sub.V4CidrBlocks {
		cidr, err := netip.ParsePrefix(raw)
		if err != nil {
			return status.Errorf(codes.Internal, "subnet has invalid cidr block %q", raw)
		}
		if cidr.Contains(addr) {
			return nil
		}
	}
	return serviceerr.InvalidArg(
		"internal_ipv4_address_spec.address",
		fmt.Sprintf("address %s is not within subnet cidr %s", address, sub.V4CidrBlocks[0]),
	)
}

// validateInternalIPv6InSubnet — v6-зеркало validateInternalIPInSubnet:
// explicit IPv6 обязан лежать в одном из v6_cidr_blocks subnet'а. Subnet 404 →
// пропуск (async NotFound через doCreate).
func (u *CreateAddressUseCase) validateInternalIPv6InSubnet(ctx context.Context, subnetID, address string) error {
	sub, err := u.subnetReader.Get(ctx, subnetID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return serviceerr.MapRepoErr(err)
	}
	if len(sub.V6CidrBlocks) == 0 {
		return status.Errorf(codes.FailedPrecondition, "subnet %s has no IPv6 CIDR", subnetID)
	}
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return serviceerr.InvalidArg("internal_ipv6_address_spec.address", "address is not a valid IP")
	}
	for _, raw := range sub.V6CidrBlocks {
		cidr, perr := netip.ParsePrefix(raw)
		if perr != nil {
			return status.Errorf(codes.Internal, "subnet has invalid cidr block %q", raw)
		}
		if cidr.Contains(addr) {
			return nil
		}
	}
	return serviceerr.InvalidArg(
		"internal_ipv6_address_spec.address",
		fmt.Sprintf("address %s is not within subnet cidr %s", address, sub.V6CidrBlocks[0]),
	)
}

// mapRequirements — общий маппинг spec-Requirements → domain для external-family
// (v4 и v6 несут одинаковый AddrRequirements). nil → nil (поле остается пустым).
func mapRequirements(r *AddrRequirements) *domain.AddressRequirements {
	if r == nil {
		return nil
	}
	return &domain.AddressRequirements{
		DdosProtectionProvider: r.DdosProtectionProvider,
		OutgoingSmtpCapability: r.OutgoingSmtpCapability,
	}
}

// checkSubnetExists — общая FK-валидация ("Subnet <X> not found") для
// internal-family (v4 и v6). Пустой subnetID пропускается (CIDR-less подсеть
// легальна). Это чтение связанного ресурса, а не side-effect — остается в
// use-case.
func (u *CreateAddressUseCase) checkSubnetExists(ctx context.Context, subnetID string) error {
	if subnetID == "" {
		return nil
	}
	if _, serr := u.subnetReader.Get(ctx, subnetID); serr != nil {
		return status.Errorf(codes.NotFound, "Subnet %s not found", subnetID)
	}
	return nil
}

// applyAddressSpec — заполняет family-specific поля domain.Address по тому
// единственному из четырех spec'ов, что задан в CreateInput (взаимоисключимость
// гарантирована валидатором Execute). Все четыре ветви идут через общие
// helper'ы (mapRequirements для external, checkSubnetExists для internal), чтобы
// новое family-инвариант-правило не пришлось дублировать по-семейно.
func (u *CreateAddressUseCase) applyAddressSpec(ctx context.Context, a *domain.Address, in CreateInput) error {
	switch {
	case in.ExternalSpec != nil:
		a.Type = domain.AddressTypeExternal
		a.IpVersion = domain.IpVersionIPv4
		a.ExternalIpv4 = &domain.ExternalIpv4Spec{
			Address:      in.ExternalSpec.Address,
			ZoneID:       in.ExternalSpec.ZoneID,
			Requirements: mapRequirements(in.ExternalSpec.Requirements),
		}
	case in.InternalSpec != nil:
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv4
		if err := u.checkSubnetExists(ctx, in.InternalSpec.SubnetID); err != nil {
			return err
		}
		a.InternalIpv4 = &domain.InternalIpv4Spec{
			Address:  in.InternalSpec.Address,
			SubnetID: in.InternalSpec.SubnetID,
		}
	case in.InternalIpv6Spec != nil:
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv6
		if err := u.checkSubnetExists(ctx, in.InternalIpv6Spec.SubnetID); err != nil {
			return err
		}
		a.InternalIpv6 = &domain.InternalIpv6Spec{
			Address:  in.InternalIpv6Spec.Address,
			SubnetID: in.InternalIpv6Spec.SubnetID,
		}
	default:
		// external IPv6: sparse counter-based allocator из глобального
		// AddressPool с v6 CIDR (cascade resolve как у v4).
		a.Type = domain.AddressTypeExternal
		a.IpVersion = domain.IpVersionIPv6
		a.ExternalIpv6 = &domain.ExternalIpv6Spec{
			Address:      in.ExternalIpv6Spec.Address,
			ZoneID:       in.ExternalIpv6Spec.ZoneID,
			Requirements: mapRequirements(in.ExternalIpv6Spec.Requirements),
		}
	}
	return nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный
// backstop: project-exists + Insert + multi-family IPAM allocation +
// outbox-emit Address.CREATED — все в одной writer-TX. Defer w.Abort() —
// при любой ошибке Insert и Allocate-side-effects откатываются автоматически,
// orphan-address window закрыт.
func (u *CreateAddressUseCase) doCreate(ctx context.Context, addrID string, in CreateInput) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, in.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "project check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Project %s not found", in.ProjectID)
	}

	a := &domain.Address{
		ID:                 addrID,
		ProjectID:          in.ProjectID,
		Name:               domain.RcNameVPC(in.Name),
		Description:        domain.RcDescription(in.Description),
		Labels:             domain.LabelsFromMap(in.Labels),
		DeletionProtection: in.DeletionProtection,
		Reserved:           true,
	}

	if err := u.applyAddressSpec(ctx, a, in); err != nil {
		return nil, err
	}

	// Резолвим external-пулы ДО открытия Writer-TX (отдельная Reader-TX).
	// Иначе resolve открывал бы вторую pool-конн под уже держимым Writer'ом —
	// при burst-нагрузке все коннекты заняты Writer'ами, ждущими Reader'ов →
	// deadlock пула. После pre-resolve внутри Writer'а остается только атомарный
	// freelist-pop по уже известному pool.ID.
	var v4Pool, v6Pool *addresspool.ResolvedPool
	if u.pools != nil {
		if a.ExternalIpv4 != nil && a.ExternalIpv4.Address == "" {
			v4Pool, err = u.pools.ResolvePoolForAddressObjFamily(ctx, &kachorepo.AddressRecord{Address: *a}, addresspool.FamilyV4)
			if err != nil {
				return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
			}
		}
		if a.ExternalIpv6 != nil && a.ExternalIpv6.Address == "" {
			v6Pool, err = u.pools.ResolvePoolForAddressObjFamily(ctx, &kachorepo.AddressRecord{Address: *a}, addresspool.FamilyV6)
			if err != nil {
				return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
			}
		}
	}

	// Открываем ОДНУ writer-TX на Insert + Allocate + Outbox — atomic.
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.Addresses().Insert(ctx, a)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}

	// Inline IPAM allocation в той же writer-TX (atomic с Insert + outbox).
	// При любой ошибке Abort откатывает Insert — compensating delete больше
	// не нужен. Пулы уже резолвлены до Writer'а (см. выше).
	if created.ExternalIpv4 != nil && created.ExternalIpv4.Address == "" && v4Pool != nil {
		res, aerr := u.allocateExternalIPv4(ctx, w, created, v4Pool)
		if aerr != nil {
			return nil, aerr
		}
		created.ExternalIpv4.Address = res.IP
		created.ExternalIpv4.AddressPoolID = res.PoolID
	}
	if u.pools != nil {
		if created.InternalIpv4 != nil && created.InternalIpv4.Address == "" && created.InternalIpv4.SubnetID != "" {
			res, aerr := u.allocateInternalIPv4(ctx, w, created)
			if aerr != nil {
				return nil, aerr
			}
			created.InternalIpv4.Address = res.IP
		}
	}
	// IPv6 internal IPAM не зависит от pools (адрес выбирается случайно внутри
	// subnet.v6_cidr_blocks[0]) — аллоцируем независимо от u.pools.
	if created.InternalIpv6 != nil && created.InternalIpv6.Address == "" && created.InternalIpv6.SubnetID != "" {
		res, aerr := u.allocateInternalIPv6(ctx, w, created)
		if aerr != nil {
			return nil, aerr
		}
		created.InternalIpv6.Address = res.IP
	}
	// External IPv6: sparse counter-based allocator. Пул резолвлен до Writer'а
	// (v6Pool).
	if created.ExternalIpv6 != nil && created.ExternalIpv6.Address == "" && v6Pool != nil {
		res, aerr := u.allocateExternalIPv6(ctx, w, created, v6Pool)
		if aerr != nil {
			return nil, aerr
		}
		created.ExternalIpv6.Address = res.IP
		created.ExternalIpv6.AddressPoolID = res.PoolID
	}

	if err := w.Outbox().Emit(ctx, "Address", created.ID, "CREATED", helpers.DomainToMap(created)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	// Публикуем INTENT на owner-tuple vpc_address→project в той же writer-TX
	// (atomic с Insert + IPAM allocate). Так intent не теряется при ошибке,
	// в отличие от best-effort emit после commit. В mirror-feed несем labels
	// Address + parent_project_id (ProjectHierarchyItem), а не голый tuple — иначе
	// resource_mirror в kacho-iam остается без labels и ARM_LABELS-селектор не
	// матчит даже свежесозданный Address. Симметрично network/subnet/securitygroup.
	items := []fgaregister.Item{
		fgaregister.ProjectHierarchyItem(in.ProjectID, "vpc_address", created.ID,
			domain.LabelsToMap(created.Labels)),
	}
	if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(items...)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	// Sync-primary owner-tuple registration (после durable commit); fail-closed.
	if u.registrar != nil {
		if err := u.registrar.Register(ctx, items); err != nil {
			return nil, err
		}
	}
	return marshalAddressRecord(created)
}

// --- Allocation helpers ------------------------------------------------------
//
// Зеркалят пути allocate-семейств (internal v4/v6, external v4/v6); живут рядом
// с create.go-use-case'ом, потому что вызываются из его inline IPAM-аллокации.
//
// Каждый helper принимает открытый Writer-TX — SetIPSpec/SetInternalIPv6/
// AllocateIPFromFreelist/AllocateExternalIPv6 идут через `w.Addresses().*`,
// atomic с Insert + Outbox в одной TX.

// allocateMaxAttempts — максимум попыток random-pick + retry-on-conflict.
// При near-full CIDR (≥95% занято) random-pick имеет high false-fail rate
// (см. allocateRandomPhase ниже). После этого порога переключаемся на
// deterministic sweep.
const allocateMaxAttempts = 32

// allocateRandomPhase — сколько попыток сделать random-pick'ом до того
// как переключиться на deterministic sweep по тем же CIDR. Random в первые
// N попыток дешевле (1 SQL/попытка), при low/medium occupancy сходится
// быстро. Переход в sweep гарантирует closure под high-occupancy.
const allocateRandomPhase = 8

// v6AllocateMaxAttempts — число попыток random-pick + retry-on-conflict для
// internal-IPv6. IPv6-подсети огромные (обычно /64), коллизии редки —
// небольшого числа попыток достаточно.
const v6AllocateMaxAttempts = 16

// allocResult — итог inline-аллокации одного семейства.
type allocResult struct {
	IP     string
	PoolID string // только для external; "" для internal
}

func (u *CreateAddressUseCase) allocateInternalIPv4(ctx context.Context, w Writer, addr *kachorepo.AddressRecord) (*allocResult, error) {
	if addr.InternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no internal_ipv4 spec", addr.ID)
	}
	if addr.InternalIpv4.Address != "" {
		return &allocResult{IP: addr.InternalIpv4.Address}, nil
	}
	if addr.InternalIpv4.SubnetID == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s internal_ipv4.subnet_id is empty", addr.ID)
	}
	sub, err := u.subnetReader.Get(ctx, addr.InternalIpv4.SubnetID)
	if err != nil {
		return nil, err
	}
	if len(sub.V4CidrBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"subnet %s has no IPv4 CIDR", sub.ID)
	}

	parsedV4Count := 0
	totalConflicts := 0
	skippedNonV4 := 0
	parseFails := 0
	for _, cidrStr := range sub.V4CidrBlocks {
		cidr, err := netip.ParsePrefix(strings.TrimSpace(cidrStr))
		if err != nil {
			parseFails++
			slog.WarnContext(ctx, "allocator: skipping unparseable subnet cidr",
				"subnet_id", sub.ID, "cidr", cidrStr, "err", err)
			continue
		}
		if !cidr.Addr().Is4() {
			skippedNonV4++
			continue
		}
		parsedV4Count++
		tried := make(map[string]struct{}, allocateMaxAttempts)
		// Phase 1: random pick.
		for attempt := 0; attempt < allocateRandomPhase; attempt++ {
			ip, err := domain.PickRandomIPv4(cidr)
			if err != nil {
				break
			}
			if _, dup := tried[ip]; dup {
				continue
			}
			tried[ip] = struct{}{}
			addr.InternalIpv4.Address = ip
			updated, err := w.Addresses().SetIPSpec(ctx, addr.ID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error",
					"subnet_id", sub.ID, "address_id", addr.ID, "ip_attempt", ip, "err", err)
				return nil, err
			}
			return &allocResult{IP: updated.InternalIpv4.Address}, nil
		}
		// Phase 2: deterministic sweep.
		for _, candidate := range domain.UsableIPv4Sweep(cidr, allocateMaxAttempts-allocateRandomPhase) {
			if _, dup := tried[candidate]; dup {
				continue
			}
			tried[candidate] = struct{}{}
			addr.InternalIpv4.Address = candidate
			updated, err := w.Addresses().SetIPSpec(ctx, addr.ID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error in sweep",
					"subnet_id", sub.ID, "address_id", addr.ID, "ip_attempt", candidate, "err", err)
				return nil, err
			}
			return &allocResult{IP: updated.InternalIpv4.Address}, nil
		}
	}
	slog.WarnContext(ctx, "allocator: subnet exhausted",
		"subnet_id", sub.ID,
		"address_id", addr.ID,
		"cidr_blocks", sub.V4CidrBlocks,
		"parsed_ipv4", parsedV4Count,
		"skipped_non_v4", skippedNonV4,
		"parse_fails", parseFails,
		"unique_conflicts", totalConflicts)
	if parsedV4Count == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"subnet %s has no IPv4 cidr_blocks (allocator requires IPv4)", sub.ID)
	}
	return nil, status.Errorf(codes.ResourceExhausted,
		"subnet %s exhausted (tried %d random + %d sweep IPs across %d cidr_blocks; %d unique-conflicts)",
		sub.ID, allocateRandomPhase, allocateMaxAttempts-allocateRandomPhase, parsedV4Count, totalConflicts)
}

func (u *CreateAddressUseCase) allocateInternalIPv6(ctx context.Context, w Writer, addr *kachorepo.AddressRecord) (*allocResult, error) {
	if addr.InternalIpv6 == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "address %s has no internal_ipv6 spec", addr.ID)
	}
	if addr.InternalIpv6.Address != "" {
		return &allocResult{IP: addr.InternalIpv6.Address}, nil
	}
	if addr.InternalIpv6.SubnetID == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "address %s internal_ipv6.subnet_id is empty", addr.ID)
	}
	sub, err := u.subnetReader.Get(ctx, addr.InternalIpv6.SubnetID)
	if err != nil {
		return nil, err
	}
	if len(sub.V6CidrBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "subnet %s has no v6_cidr_blocks", sub.ID)
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(sub.V6CidrBlocks[0]))
	if err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
		return nil, status.Errorf(codes.FailedPrecondition, "subnet %s has invalid v6 cidr block %q", sub.ID, sub.V6CidrBlocks[0])
	}
	tried := make(map[string]struct{}, v6AllocateMaxAttempts)
	conflicts := 0
	for attempt := 0; attempt < v6AllocateMaxAttempts; attempt++ {
		ip, perr := domain.PickRandomIPv6(prefix)
		if perr != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "subnet %s: cannot pick IPv6 in %s: %v", sub.ID, prefix, perr)
		}
		if _, dup := tried[ip]; dup {
			continue
		}
		tried[ip] = struct{}{}
		addr.InternalIpv6.Address = ip
		updated, uerr := w.Addresses().SetInternalIPv6(ctx, addr.ID, addr.InternalIpv6)
		if uerr != nil {
			if isUniqueViolation(uerr) {
				conflicts++
				addr.InternalIpv6.Address = ""
				continue
			}
			slog.ErrorContext(ctx, "v6 allocator: SetInternalIPv6 returned non-conflict error",
				"subnet_id", sub.ID, "address_id", addr.ID, "ip_attempt", ip, "err", uerr)
			return nil, uerr
		}
		return &allocResult{IP: updated.InternalIpv6.Address}, nil
	}
	slog.WarnContext(ctx, "v6 allocator: exhausted attempts",
		"subnet_id", sub.ID, "address_id", addr.ID, "cidr", prefix.String(), "conflicts", conflicts)
	return nil, status.Errorf(codes.ResourceExhausted,
		"subnet %s: could not allocate a free IPv6 in %s after %d attempts (%d unique-conflicts)",
		sub.ID, prefix, v6AllocateMaxAttempts, conflicts)
}

func (u *CreateAddressUseCase) allocateExternalIPv4(ctx context.Context, w Writer, addr *kachorepo.AddressRecord, resolved *addresspool.ResolvedPool) (*allocResult, error) {
	if addr.ExternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no external_ipv4 spec", addr.ID)
	}
	if addr.ExternalIpv4.Address != "" {
		return &allocResult{
			IP:     addr.ExternalIpv4.Address,
			PoolID: addr.ExternalIpv4.AddressPoolID,
		}, nil
	}
	pool := resolved.Pool
	if len(pool.V4CIDRBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address pool %s has no v4_cidr_blocks", pool.ID)
	}

	ip, err := w.Addresses().AllocateIPFromFreelist(ctx, pool.ID, addr.ID)
	if err != nil {
		if errors.Is(err, repo.ErrPoolExhausted) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"address pool %s exhausted", pool.ID)
		}
		slog.ErrorContext(ctx, "allocator: AllocateIPFromFreelist failed",
			"pool_id", pool.ID, "address_id", addr.ID, "err", err)
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: allocate from freelist: %v", repo.ErrInternal, err))
	}
	return &allocResult{IP: ip, PoolID: pool.ID}, nil
}

func (u *CreateAddressUseCase) allocateExternalIPv6(ctx context.Context, w Writer, addr *kachorepo.AddressRecord, resolved *addresspool.ResolvedPool) (*allocResult, error) {
	if addr.ExternalIpv6 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no external_ipv6 spec", addr.ID)
	}
	if addr.ExternalIpv6.Address != "" {
		return &allocResult{
			IP:     addr.ExternalIpv6.Address,
			PoolID: addr.ExternalIpv6.AddressPoolID,
		}, nil
	}
	pool := resolved.Pool
	if len(pool.V6CIDRBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address pool %s has no v6_cidr_blocks", pool.ID)
	}

	ip, err := w.Addresses().AllocateExternalIPv6(ctx, pool.ID, addr.ID, addr.ExternalIpv6.ZoneID)
	if err != nil {
		if errors.Is(err, repo.ErrPoolExhausted) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"address pool %s exhausted (ipv6)", pool.ID)
		}
		if errors.Is(err, repo.ErrFailedPrecondition) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"%s", strings.TrimPrefix(err.Error(), repo.ErrFailedPrecondition.Error()+": "))
		}
		slog.ErrorContext(ctx, "allocator: AllocateExternalIPv6 failed",
			"pool_id", pool.ID, "address_id", addr.ID, "err", err)
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: allocate external ipv6: %v", repo.ErrInternal, err))
	}
	return &allocResult{IP: ip, PoolID: pool.ID}, nil
}
