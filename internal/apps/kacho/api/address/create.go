package address

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgawrite"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
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
// Orthogonal: композирует поля Address-запроса + четыре family-specific spec'а
// (External v4/v6, Internal v4/v6) — это **не** тривиальная обёртка вокруг
// domain.Address. Skill evgeniy §2 B.3 / §7 I.1 разрешают композицию domain.X +
// orthogonal extra fields; запрет — на parallel-структуры `struct{X domain.X}`
// без других полей. Здесь spec'и не могут быть смержены в domain.Address —
// там oneof выражен через указатели, и валидация семейств через nil-проверки
// плоского CreateInput чище. См. также KAC-94: тривиальные обёртки удалены в
// Network/Subnet/Gateway/RouteTable/SecurityGroup/PrivateEndpoint.
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
	// Для external IPv6 (KAC-60, если ExternalIpv6Spec != nil):
	ExternalIpv6Spec *ExternalAddrSpec
}

// CreateAddressUseCase инициирует создание Address (multi-family). Sync-
// проверки (folder exists, subnet exists, name unique, explicit-IP-in-CIDR)
// выполняются ДО создания Operation — клиент получает fast-fail gRPC-status,
// а не «200 + операция, упавшая через секунду» (см. kacho-vpc#8). Async-часть
// (`doCreate`) — собирает domain.Address по family, Insert, затем inline IPAM
// allocation (AllocateExternalIP/AllocateInternalIP/AllocateInternalIPv6/
// AllocateExternalIPv6).
//
// pools может быть nil в test-setup'ах — тогда v4/v6-external allocate
// недоступен, IP в результате остаётся пустым (Allocate*IP возвращает
// Unavailable). v6-internal allocate не зависит от pools — он работает
// random-pick'ом внутри subnet.v6_cidr_blocks.
//
// A.7 sub-PR 2 (KAC-94, skill evgeniy §6 G.5 / I.9 / I.10): worker открывает
// ОДНУ writer-TX и делает в ней Insert(Address) → Allocate*IP (через writer.
// Addresses().Set*Spec / AllocateIPFromFreelist / AllocateExternalIPv6) →
// Outbox.Emit Address.CREATED. Либо весь композит виден (Commit), либо ничего
// (Abort/crash) — orphan-address-without-allocated-IP window закрыт. Старый
// compensating delete-after-failure (отдельный TX) больше не нужен — Abort
// автоматически снимает Insert.
type CreateAddressUseCase struct {
	repo          Repo
	subnetReader  SubnetReader
	projectClient ProjectClient
	opsRepo       operations.Repo
	pools         PoolService // nil → external IPAM недоступна (test-only)

	// fgaWriter / logger — KAC-127 issue #22: publish
	// `vpc_address:<id>#project@project:<project_id>` after commit.
	fgaWriter fgawrite.HierarchyTupleWriter
	logger    *slog.Logger
}

// NewCreateAddressUseCase создаёт CreateAddressUseCase.
func NewCreateAddressUseCase(r Repo, subnetReader SubnetReader, projectClient ProjectClient, opsRepo operations.Repo, pools PoolService) *CreateAddressUseCase {
	return &CreateAddressUseCase{
		repo:          r,
		subnetReader:  subnetReader,
		projectClient: projectClient,
		opsRepo:       opsRepo,
		pools:         pools,
	}
}

// WithFGAWriter wires the OpenFGA hierarchy-tuple writer (KAC-127 issue #22).
func (u *CreateAddressUseCase) WithFGAWriter(w fgawrite.HierarchyTupleWriter, logger *slog.Logger) *CreateAddressUseCase {
	u.fgaWriter = w
	u.logger = logger
	return u
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

	// Domain self-validation (skill evgeniy §4 D.5 / AP-1): NameVPC /
	// Description / Labels через newtypes — use-case больше НЕ зовёт
	// corevalidate.* напрямую (выполняется через Address.Validate()).
	// VPC Address: empty name allowed (verbatim YC permissive policy).
	addrForValidate := domain.Address{
		Name:        domain.RcNameVPC(in.Name),
		Description: domain.RcDescription(in.Description),
		Labels:      domain.LabelsFromMap(in.Labels),
	}
	if err := addrForValidate.Validate(); err != nil {
		return nil, err
	}

	// Verbatim YC: requirements.ddos_protection_provider только из whitelist;
	// requirements.outgoing_smtp_capability — только пустое (probe 2026-05-04).
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

	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.
	// Sync subnet/uniqueness-проверки (через DB-state в той же сервис-БД)
	// остаются — они race-free относительно peer-сервисов.
	if in.InternalSpec != nil && in.InternalSpec.SubnetID != "" {
		if _, err := u.subnetReader.Get(ctx, in.InternalSpec.SubnetID); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", in.InternalSpec.SubnetID)
			}
			return nil, mapRepoErr(err)
		}
	}
	if in.InternalIpv6Spec != nil && in.InternalIpv6Spec.SubnetID != "" {
		if _, err := u.subnetReader.Get(ctx, in.InternalIpv6Spec.SubnetID); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", in.InternalIpv6Spec.SubnetID)
			}
			return nil, mapRepoErr(err)
		}
	}
	if in.Name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		existing, _, lerr := rd.Addresses().List(ctx, AddressFilter{ProjectID: in.ProjectID, Name: in.Name}, Pagination{})
		_ = rd.Close()
		if lerr != nil {
			return nil, mapRepoErr(lerr)
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

	addrID := ids.NewID(ids.PrefixAddress)
	op, err := operations.New(
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
// (NotFound будет async через doCreate, как и для всех остальных FK; verbatim
// YC). Любая другая ошибка чтения subnetReader → Internal: pass-through.
func (u *CreateAddressUseCase) validateInternalIPInSubnet(ctx context.Context, subnetID, address string) error {
	sub, err := u.subnetReader.Get(ctx, subnetID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			// Subnet 404 — async; не нарушаем YC-DIFF-INVALID-PARENT-CODE.
			return nil
		}
		return mapRepoErr(err)
	}
	if len(sub.V4CidrBlocks) == 0 {
		// CIDR-less subnet (см. kacho-proto#8 — v4_cidr_blocks больше не required
		// на Subnet.Create): нельзя ни валидировать explicit address, ни выделить
		// internal IPv4 в такой подсети.
		return status.Errorf(codes.FailedPrecondition, "subnet %s has no IPv4 CIDR", subnetID)
	}
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return invalidArg(
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
	return invalidArg(
		"internal_ipv4_address_spec.address",
		fmt.Sprintf("address %s is not within subnet cidr %s", address, sub.V4CidrBlocks[0]),
	)
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный
// backstop: folder-exists + Insert + multi-family IPAM allocation +
// outbox-emit Address.CREATED — всё в одной writer-TX. Defer w.Abort() —
// при любой ошибке Insert и Allocate-side-effects откатываются автоматически,
// orphan-address window закрыт.
func (u *CreateAddressUseCase) doCreate(ctx context.Context, addrID string, in CreateInput) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, in.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		// verbatim YC text: "Folder with id <X> not found".
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", in.ProjectID)
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

	if in.ExternalSpec != nil {
		a.Type = domain.AddressTypeExternal
		a.IpVersion = domain.IpVersionIPv4
		a.ExternalIpv4 = &domain.ExternalIpv4Spec{
			Address: in.ExternalSpec.Address,
			ZoneID:  in.ExternalSpec.ZoneID,
		}
		if r := in.ExternalSpec.Requirements; r != nil {
			a.ExternalIpv4.Requirements = &domain.AddressRequirements{
				DdosProtectionProvider: r.DdosProtectionProvider,
				OutgoingSmtpCapability: r.OutgoingSmtpCapability,
			}
		}
	} else if in.InternalSpec != nil {
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv4
		subnetID := in.InternalSpec.SubnetID
		// FK-валидация (verbatim YC text "Subnet <X> not found"). Она в
		// use-case остаётся — потому что относится к чтению связанного
		// ресурса, а не к side-effect.
		if subnetID != "" {
			if _, serr := u.subnetReader.Get(ctx, subnetID); serr != nil {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", subnetID)
			}
		}
		a.InternalIpv4 = &domain.InternalIpv4Spec{
			Address:  in.InternalSpec.Address,
			SubnetID: subnetID,
		}
	} else if in.InternalIpv6Spec != nil {
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv6
		subnetID := in.InternalIpv6Spec.SubnetID
		if subnetID != "" {
			if _, serr := u.subnetReader.Get(ctx, subnetID); serr != nil {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", subnetID)
			}
		}
		a.InternalIpv6 = &domain.InternalIpv6Spec{
			Address:  in.InternalIpv6Spec.Address,
			SubnetID: subnetID,
		}
	} else {
		// external IPv6 (KAC-60). Sparse counter-based allocator из глобального
		// AddressPool с v6 CIDR (cascade resolve как у v4).
		a.Type = domain.AddressTypeExternal
		a.IpVersion = domain.IpVersionIPv6
		a.ExternalIpv6 = &domain.ExternalIpv6Spec{
			Address: in.ExternalIpv6Spec.Address,
			ZoneID:  in.ExternalIpv6Spec.ZoneID,
		}
		if r := in.ExternalIpv6Spec.Requirements; r != nil {
			a.ExternalIpv6.Requirements = &domain.AddressRequirements{
				DdosProtectionProvider: r.DdosProtectionProvider,
				OutgoingSmtpCapability: r.OutgoingSmtpCapability,
			}
		}
	}

	// Открываем ОДНУ writer-TX на Insert + Allocate + Outbox — atomic.
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.Addresses().Insert(ctx, a)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	// Inline IPAM allocation в той же writer-TX (atomic с Insert + outbox).
	// При любой ошибке Abort откатывает Insert — compensating delete больше
	// не нужен.
	if u.pools != nil {
		if created.ExternalIpv4 != nil && created.ExternalIpv4.Address == "" {
			res, aerr := u.allocateExternalIPv4(ctx, w, created)
			if aerr != nil {
				return nil, aerr
			}
			created.ExternalIpv4.Address = res.IP
			created.ExternalIpv4.AddressPoolID = res.PoolID
		}
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
	// External IPv6 (KAC-60): sparse counter-based allocator. Полагается на
	// pools (cascade resolve по zone/labels — как у v4).
	if u.pools != nil && created.ExternalIpv6 != nil && created.ExternalIpv6.Address == "" {
		res, aerr := u.allocateExternalIPv6(ctx, w, created)
		if aerr != nil {
			return nil, aerr
		}
		created.ExternalIpv6.Address = res.IP
		created.ExternalIpv6.AddressPoolID = res.PoolID
	}

	if err := w.Outbox().Emit(ctx, "Address", created.ID, "CREATED", addressPayloadMap(created)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	// KAC-127 issue #22: publish vpc_address→project hierarchy tuple.
	fgawrite.Emit(ctx, u.fgaWriter, u.logger, "vpc_address", created.ID, in.ProjectID)
	return marshalAddressRecord(created)
}

// --- Allocation helpers ------------------------------------------------------
//
// These mirror addressref → addresspool path / AllocateInternalIPv6 /
// AllocateExternalIP / AllocateExternalIPv6 — kept inside the create.go use-case
// for now (Wave 3 scope is moving the user-facing CRUD; internal-only allocate
// RPC continues to live in `internal/service/address.go::AddressService` until
// the internal handler migrates to its own UC package).
//
// A.7 sub-PR 2 (KAC-94): allocation helpers принимают открытый Writer-TX —
// SetIPSpec/SetInternalIPv6/AllocateIPFromFreelist/AllocateExternalIPv6 идут
// через `w.Addresses().*`, atomic с Insert + Outbox в одной TX.

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

// allocResult — local copy of allocResult.
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
			ip, err := pickRandomIPv4(cidr)
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
		for _, candidate := range usableIPv4Sweep(cidr, allocateMaxAttempts-allocateRandomPhase) {
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
		ip, perr := pickRandomIPv6(prefix)
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

func (u *CreateAddressUseCase) allocateExternalIPv4(ctx context.Context, w Writer, addr *kachorepo.AddressRecord) (*allocResult, error) {
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
	resolved, err := u.pools.ResolvePoolForAddressObjFamily(ctx, addr, addresspool.FamilyV4)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
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
		return nil, status.Errorf(codes.Internal, "allocate from freelist: %v", err)
	}
	return &allocResult{IP: ip, PoolID: pool.ID}, nil
}

func (u *CreateAddressUseCase) allocateExternalIPv6(ctx context.Context, w Writer, addr *kachorepo.AddressRecord) (*allocResult, error) {
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
	resolved, err := u.pools.ResolvePoolForAddressObjFamily(ctx, addr, addresspool.FamilyV6)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
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
		return nil, status.Errorf(codes.Internal, "allocate external ipv6: %v", err)
	}
	return &allocResult{IP: ip, PoolID: pool.ID}, nil
}

// usableIPv4Sweep — deterministic enumeration usable IPv4 в CIDR (без
// network/broadcast). Используется в Phase 2 allocator'а для гарантии closure
// когда random-pick не сходится. Cap'ируется maxN чтобы не аллокировать
// миллионы строк для больших CIDR; для /28 (14 IP) maxN=24 достаточно.
func usableIPv4Sweep(cidr netip.Prefix, maxN int) []string {
	if !cidr.Addr().Is4() {
		return nil
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	if hostBits >= 32 {
		return nil
	}
	total := uint32(1) << hostBits
	first := uint32(1)
	last := total - 1
	switch hostBits {
	case 0:
		first, last = 0, 1
	case 1:
		first, last = 0, 2
	}
	if uint32(maxN) < last-first {
		last = first + uint32(maxN)
	}
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	out := make([]string, 0, last-first)
	for i := first; i < last; i++ {
		var ipBytes [4]byte
		binary.BigEndian.PutUint32(ipBytes[:], baseInt+i)
		out = append(out, net.IP(ipBytes[:]).String())
	}
	return out
}

// pickRandomIPv4 выбирает random IP из CIDR, исключая network/broadcast addresses
// (для prefix length < 31). Использует crypto/rand для unpredictable allocation.
//
// Edge cases (R8 fix /31 off-by-one):
//   - /32 (hostBits=0): единственный адрес — base.
//   - /31 (hostBits=1): оба адреса валидны (point-to-point) — base+0 или base+1.
//   - /≤30 (hostBits≥2): пропускаем .0 (network) и .last (broadcast) →
//     offset в [1, maxHosts].
func pickRandomIPv4(cidr netip.Prefix) (string, error) {
	if !cidr.Addr().Is4() {
		return "", repo.ErrInvalidIPv4
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	var offset uint32
	switch hostBits {
	case 0:
		return cidr.Addr().String(), nil
	case 1:
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return "", err
		}
		offset = binary.BigEndian.Uint32(randBytes[:]) % 2
	default:
		maxHosts := uint32(1<<hostBits) - 2
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return "", err
		}
		offset = binary.BigEndian.Uint32(randBytes[:])%maxHosts + 1
	}
	var ipBytes [4]byte
	binary.BigEndian.PutUint32(ipBytes[:], baseInt+offset)
	return net.IP(ipBytes[:]).String(), nil
}

// pickRandomIPv6 выбирает случайный адрес внутри IPv6-префикса, заполняя
// host-биты криптослучайными значениями. Пропускает all-zeros host (subnet-router
// anycast `<prefix>::`); для очень узких префиксов (/127, /128) ведёт себя
// детерминированно (там почти нет выбора).
func pickRandomIPv6(prefix netip.Prefix) (string, error) {
	addr := prefix.Masked().Addr()
	base := addr.As16()
	bits := prefix.Bits()
	hostBits := 128 - bits
	if hostBits <= 0 {
		return addr.String(), nil
	}
	var rnd [16]byte
	for try := 0; try < 8; try++ {
		if _, err := rand.Read(rnd[:]); err != nil {
			return "", err
		}
		out := base
		for i := 0; i < 16; i++ {
			bitIndex := i * 8
			if bitIndex+8 <= bits {
				continue
			}
			var mask byte
			if bitIndex >= bits {
				mask = 0xff
			} else {
				keep := bits - bitIndex
				mask = byte(0xff >> keep)
			}
			out[i] = (base[i] &^ mask) | (rnd[i] & mask)
		}
		cand := netip.AddrFrom16(out)
		if cand == addr {
			continue
		}
		return cand.String(), nil
	}
	return addr.String(), nil
}
