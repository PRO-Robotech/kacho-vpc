package service

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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
)

// CreateAddressReq — запрос на создание адреса.
type CreateAddressReq struct {
	FolderID           string
	Name               string
	Description        string
	Labels             map[string]string
	DeletionProtection bool
	// Для external (если ExternalSpec != nil):
	ExternalSpec *ExternalAddrSpec
	// Для internal IPv4 (если InternalSpec != nil):
	InternalSpec *InternalAddrSpec
	// Для internal IPv6 (если InternalIpv6Spec != nil):
	InternalIpv6Spec *InternalAddrSpec
}

// ExternalAddrSpec — спецификация внешнего адреса.
type ExternalAddrSpec struct {
	Address      string
	ZoneID       string
	Requirements *AddrRequirements
}

// AddrRequirements — параметры внешнего IP (DDoS, SMTP).
type AddrRequirements struct {
	DdosProtectionProvider string
	OutgoingSmtpCapability string
}

// InternalAddrSpec — спецификация внутреннего адреса.
type InternalAddrSpec struct {
	Address  string
	SubnetID string
}

// UpdateAddressReq — запрос на обновление адреса.
type UpdateAddressReq struct {
	AddressID          string
	Name               string
	Description        string
	Labels             map[string]string
	DeletionProtection bool
	Reserved           bool
	UpdateMask         []string
}

// AddressService — бизнес-логика управления IP-адресами + inline IPAM.
//
// Allocate*IP методы (см. ниже в этом файле) выделяют IP в request-path
// атомарно через UNIQUE constraints на addresses (external_ip_uniq /
// internal_subnet_ip_uniq) с bounded retry. Если pools == nil — Allocate*
// возвращают Unavailable (test-setup без IPAM).
type AddressService struct {
	repo         AddressRepo
	subnetRepo   SubnetRepo
	folderClient FolderClient
	opsRepo      operations.Repo
	pools        *AddressPoolService // inline IPAM; nil → Allocate* недоступны
}

// NewAddressService создаёт AddressService с inline IPAM (pools может быть nil
// в test-setup'ах — тогда IP в Create-результате остаётся пустым).
func NewAddressService(repo AddressRepo, subnetRepo SubnetRepo, folderClient FolderClient, opsRepo operations.Repo, pools *AddressPoolService) *AddressService {
	return &AddressService{repo: repo, subnetRepo: subnetRepo, folderClient: folderClient, opsRepo: opsRepo, pools: pools}
}

// validateInternalIPInSubnet проверяет sync-ом, что explicit IP лежит в CIDR
// одной из v4_cidr_blocks указанной subnet. Если subnet не найден — пропуск
// (NotFound будет async через doCreate, как и для всех остальных FK; verbatim
// YC). Любая другая ошибка чтения subnetRepo → Internal: pass-through.
func (s *AddressService) validateInternalIPInSubnet(ctx context.Context, subnetID, address string) error {
	sub, err := s.subnetRepo.Get(ctx, subnetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
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
			// Внутренняя ошибка: subnet с невалидным CIDR — нечего считать
			// match-ем, но и валидацию пропустить нельзя (это бы дало false
			// positive). Маркируем как Internal.
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

// GetByValue возвращает Address по его IP-значению (external или internal).
//
// YC verbatim:
//   - oneof address: external_ipv4_address или internal_ipv4_address.
//   - oneof scope (опционально): subnet_id (для уточнения).
//   - При не-существовании → NotFound.
func (s *AddressService) GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*domain.Address, error) {
	if externalIP == "" && internalIP == "" {
		return nil, invalidArg("address", "address (external_ipv4_address or internal_ipv4_address) is required")
	}
	a, err := s.repo.GetByValue(ctx, externalIP, internalIP, subnetID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	s.loadUsedBy(ctx, []*domain.Address{a})
	return a, nil
}

// loadUsedBy обогащает каждый адрес из набора полем UsedBy (referrer-tracking,
// output-only) — кто использует адрес. Best-effort: ошибка чтения
// address_references → лог + адреса без UsedBy (graceful degradation, не валит
// чтение). Пустой/nil вход — no-op.
func (s *AddressService) loadUsedBy(ctx context.Context, addrs []*domain.Address) {
	if len(addrs) == 0 {
		return
	}
	idsList := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a != nil {
			idsList = append(idsList, a.ID)
		}
	}
	if len(idsList) == 0 {
		return
	}
	refs, err := s.repo.ReferencesForAddresses(ctx, idsList)
	if err != nil {
		slog.WarnContext(ctx, "failed to load address referrers (used_by); returning addresses without it", "err", err)
		return
	}
	for _, a := range addrs {
		if a == nil {
			continue
		}
		if ref, ok := refs[a.ID]; ok && ref != nil {
			a.UsedBy = []*domain.AddressReference{ref}
		}
	}
}

// ListBySubnet возвращает Address-ы, привязанные к указанной подсети.
//
// Использует subnetRepo.AddressesBySubnet (joining через internal_ipv4.subnet_id).
func (s *AddressService) ListBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*domain.Address, string, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, subnetID); err != nil {
		return nil, "", err
	}
	if subnetID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if _, err := s.subnetRepo.Get(ctx, subnetID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	addrs, nextToken, err := s.subnetRepo.AddressesBySubnet(ctx, subnetID, p)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	s.loadUsedBy(ctx, addrs)
	return addrs, nextToken, nil
}

// Get возвращает Address по ID.
func (s *AddressService) Get(ctx context.Context, id string) (*domain.Address, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	s.loadUsedBy(ctx, []*domain.Address{a})
	return a, nil
}

// List возвращает список адресов.
// folder_id обязателен (R10 #C1 closure).
func (s *AddressService) List(ctx context.Context, f AddressFilter, p Pagination) ([]*domain.Address, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	addrs, nextToken, err := s.repo.List(ctx, f, p)
	if err != nil {
		return nil, "", err
	}
	s.loadUsedBy(ctx, addrs)
	return addrs, nextToken, nil
}

// Create инициирует создание Address.
func (s *AddressService) Create(ctx context.Context, req CreateAddressReq) (*operations.Operation, error) {
	if req.InternalSpec != nil {
		if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, req.InternalSpec.SubnetID); err != nil {
			return nil, err
		}
	}
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.InternalIpv6Spec != nil {
		if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, req.InternalIpv6Spec.SubnetID); err != nil {
			return nil, err
		}
	}
	if req.ExternalSpec == nil && req.InternalSpec == nil && req.InternalIpv6Spec == nil {
		return nil, status.Error(codes.InvalidArgument, "address_spec required")
	}
	// folder_id / subnet_id больше НЕ валидируются sync — async через
	// folderClient.Exists / subnetRepo.Get → NotFound (verbatim YC). См.
	// YC-DIFF-INVALID-PARENT-CODE.md.
	// VPC Address: empty name allowed (verbatim YC permissive policy).
	if err := corevalidate.NameVPC("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}
	// Verbatim YC: requirements.ddos_protection_provider только из whitelist;
	// requirements.outgoing_smtp_capability — только пустое (probe 2026-05-04).
	if req.ExternalSpec != nil && req.ExternalSpec.Requirements != nil {
		if err := corevalidate.DdosProvider(
			"external_ipv4_address_spec.requirements.ddos_protection_provider",
			req.ExternalSpec.Requirements.DdosProtectionProvider,
		); err != nil {
			return nil, err
		}
		if err := corevalidate.SmtpCapability(
			"external_ipv4_address_spec.requirements.outgoing_smtp_capability",
			req.ExternalSpec.Requirements.OutgoingSmtpCapability,
		); err != nil {
			return nil, err
		}
	}

	// Verbatim YC: existence / uniqueness checks run synchronously, BEFORE the
	// Operation. The folder check / FK reads in doCreate stay as defensive
	// copies. См. kacho-vpc#8.
	if err := checkFolderExists(ctx, s.folderClient, req.FolderID); err != nil {
		return nil, err
	}
	if req.InternalSpec != nil && req.InternalSpec.SubnetID != "" {
		if _, err := s.subnetRepo.Get(ctx, req.InternalSpec.SubnetID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", req.InternalSpec.SubnetID)
			}
			return nil, mapRepoErr(err)
		}
	}
	if req.InternalIpv6Spec != nil && req.InternalIpv6Spec.SubnetID != "" {
		if _, err := s.subnetRepo.Get(ctx, req.InternalIpv6Spec.SubnetID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", req.InternalIpv6Spec.SubnetID)
			}
			return nil, mapRepoErr(err)
		}
	}
	if req.Name != "" {
		existing, _, lerr := s.repo.List(ctx, AddressFilter{FolderID: req.FolderID, Name: req.Name}, Pagination{})
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "Address with name %s already exists", req.Name)
		}
	}

	// Sync-проверка: explicit IP (`internal_ipv4_address_spec.address`) должен
	// принадлежать CIDR-блоку указанной subnet. Если IP вне CIDR — возвращаем
	// sync InvalidArgument: иначе адрес попадает в БД минуя любые FK, что
	// приводит к мусору в IPAM.
	if req.InternalSpec != nil && req.InternalSpec.SubnetID != "" && req.InternalSpec.Address != "" {
		if err := s.validateInternalIPInSubnet(ctx, req.InternalSpec.SubnetID, req.InternalSpec.Address); err != nil {
			return nil, err
		}
	}

	addrID := ids.NewID(ids.PrefixAddress)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create address %s", req.Name),
		&vpcv1.CreateAddressMetadata{AddressId: addrID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, addrID, req)
	})

	return &op, nil
}

func (s *AddressService) doCreate(ctx context.Context, addrID string, req CreateAddressReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		// verbatim YC text: "Folder with id <X> not found".
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
	}

	a := &domain.Address{
		ID:                 addrID,
		FolderID:           req.FolderID,
		CreatedAt:          time.Now().UTC(),
		Name:               req.Name,
		Description:        req.Description,
		Labels:             req.Labels,
		DeletionProtection: req.DeletionProtection,
		Reserved:           true,
	}

	if req.ExternalSpec != nil {
		a.Type = domain.AddressTypeExternal
		a.IpVersion = domain.IpVersionIPv4
		// Записываем то, что пришло от клиента; если address пустой —
		// inline allocator выделит IP ниже (см. блок `if s.pools != nil`).
		a.ExternalIpv4 = &domain.ExternalIpv4Spec{
			Address: req.ExternalSpec.Address,
			ZoneID:  req.ExternalSpec.ZoneID,
		}
		if r := req.ExternalSpec.Requirements; r != nil {
			a.ExternalIpv4.Requirements = &domain.AddressRequirements{
				DdosProtectionProvider: r.DdosProtectionProvider,
				OutgoingSmtpCapability: r.OutgoingSmtpCapability,
			}
		}
	} else if req.InternalSpec != nil {
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv4
		subnetID := req.InternalSpec.SubnetID
		// FK-валидация (verbatim YC text "Subnet <X> not found"). Она в
		// сервисе остаётся — потому что относится к чтению связанного
		// ресурса, а не к side-effect.
		if subnetID != "" {
			if _, serr := s.subnetRepo.Get(ctx, subnetID); serr != nil {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", subnetID)
			}
		}
		// Pure: если client передал internal IP — пишем; иначе пусто,
		// аллоцирует controller.
		a.InternalIpv4 = &domain.InternalIpv4Spec{
			Address:  req.InternalSpec.Address,
			SubnetID: subnetID,
		}
	} else {
		// internal IPv6 Address.
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv6
		subnetID := req.InternalIpv6Spec.SubnetID
		if subnetID != "" {
			if _, serr := s.subnetRepo.Get(ctx, subnetID); serr != nil {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", subnetID)
			}
		}
		a.InternalIpv6 = &domain.InternalIpv6Spec{
			Address:  req.InternalIpv6Spec.Address,
			SubnetID: subnetID,
		}
	}

	created, err := s.repo.Insert(ctx, a)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	// Inline IPAM allocation. Idempotent: если IP уже выставлен — Allocate*
	// возвращает существующий. На allocate-failure делаем compensating delete,
	// иначе failed Operation оставит dangling Address в БД.
	if s.pools != nil {
		if created.ExternalIpv4 != nil && created.ExternalIpv4.Address == "" {
			res, aerr := s.AllocateExternalIP(ctx, created.ID)
			if aerr != nil {
				s.compensatingDelete(ctx, created.ID, "external", aerr)
				return nil, aerr
			}
			created.ExternalIpv4.Address = res.IP
			created.ExternalIpv4.AddressPoolID = res.PoolID
		}
		if created.InternalIpv4 != nil && created.InternalIpv4.Address == "" && created.InternalIpv4.SubnetID != "" {
			res, aerr := s.AllocateInternalIP(ctx, created.ID)
			if aerr != nil {
				s.compensatingDelete(ctx, created.ID, "internal", aerr)
				return nil, aerr
			}
			created.InternalIpv4.Address = res.IP
		}
	}
	// IPv6 IPAM не зависит от address-pool'ов (адрес выбирается случайно внутри
	// subnet.v6_cidr_blocks[0]) — аллоцируем независимо от s.pools.
	if created.InternalIpv6 != nil && created.InternalIpv6.Address == "" && created.InternalIpv6.SubnetID != "" {
		res, aerr := s.AllocateInternalIPv6(ctx, created.ID)
		if aerr != nil {
			s.compensatingDelete(ctx, created.ID, "internal_ipv6", aerr)
			return nil, aerr
		}
		created.InternalIpv6.Address = res.IP
	}
	return anypb.New(protoconv.Address(created))
}

// compensatingDelete — undo Insert при failed allocate. Использует fresh
// background ctx чтобы delete отработал даже если caller отменил orig ctx.
// Failure delete'а — log-only: caller всё равно получает orig allocate-error,
// orphan address будет подобран garbage-collector'ом / ручным cleanup'ом.
func (s *AddressService) compensatingDelete(ctx context.Context, addressID, kind string, origErr error) {
	delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if delErr := s.repo.Delete(delCtx, addressID); delErr != nil {
		slog.WarnContext(ctx, "compensating delete failed after allocate error — orphan address",
			"address_id", addressID,
			"kind", kind,
			"orig_err", origErr,
			"delete_err", delErr)
	} else {
		slog.InfoContext(ctx, "compensating delete after allocate failure",
			"address_id", addressID, "kind", kind, "orig_err", origErr)
	}
}

// Update обновляет Address.
//
// Sync-валидация: см. validateAddressUpdate. Address — особый случай: name
// может быть пустым (как и в Create), потому что для адресов name необязательное.
func (s *AddressService) Update(ctx context.Context, req UpdateAddressReq) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, req.AddressID); err != nil {
		return nil, err
	}
	if req.AddressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if err := validateAddressUpdate(req); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update address %s", req.AddressID),
		&vpcv1.UpdateAddressMetadata{AddressId: req.AddressID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doUpdate(ctx, req)
	})

	return &op, nil
}

func (s *AddressService) doUpdate(ctx context.Context, req UpdateAddressReq) (*anypb.Any, error) {
	a, err := s.repo.Get(ctx, req.AddressID)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	applyAddressMask(a, req)

	updated, err := s.repo.Update(ctx, a)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(protoconv.Address(updated))
}

// validateAddressUpdate проверяет name/description/labels в Update Address.
//
// В отличие от Network/Cloud/Folder/Subnet, name для Address optional —
// `name=""` валиден, regex применяется только если непустой.
func validateAddressUpdate(req UpdateAddressReq) error {
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {},
		"deletion_protection": {}, "reserved": {},
	}
	if err := corevalidate.UpdateMask("update_mask", req.UpdateMask, known); err != nil {
		return err
	}
	updates := req.UpdateMask
	if len(updates) == 0 {
		updates = []string{"name", "description", "labels"}
	}
	for _, f := range updates {
		switch f {
		case "name":
			// VPC Address: empty name allowed (YC permissive policy).
			if err := corevalidate.NameVPC("name", req.Name); err != nil {
				return err
			}
		case "description":
			if err := corevalidate.Description("description", req.Description); err != nil {
				return err
			}
		case "labels":
			if err := corevalidate.Labels("labels", req.Labels); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyAddressMask(a *domain.Address, req UpdateAddressReq) {
	if len(req.UpdateMask) == 0 {
		a.Name = req.Name
		a.Description = req.Description
		a.Labels = req.Labels
		a.DeletionProtection = req.DeletionProtection
		a.Reserved = req.Reserved
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			a.Name = req.Name
		case "description":
			a.Description = req.Description
		case "labels":
			a.Labels = req.Labels
		case "deletion_protection":
			a.DeletionProtection = req.DeletionProtection
		case "reserved":
			a.Reserved = req.Reserved
		}
	}
}

// Delete удаляет Address.
//
// Sync-проверка `deletion_protection`: если адрес помечен как
// deletion_protection=true, мутация запрещена сразу до запуска Operation
// (FAILED_PRECONDITION). Это verbatim YC contract — клиент должен сначала
// снять защиту PATCH-ем.
//
// ListOperations возвращает операции для конкретного Address.
func (s *AddressService) ListOperations(ctx context.Context, addressID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, addressID); err != nil {
		return nil, "", err
	}
	if _, err := s.repo.Get(ctx, addressID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: addressID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}

// Move инициирует перенос Address в другой folder.
func (s *AddressService) Move(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	cur, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := checkMoveDestination(ctx, s.folderClient, cur.FolderID, destFolderID); err != nil {
		return nil, err
	}
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Move address %s", id),
		&vpcv1.MoveAddressMetadata{AddressId: id})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, err := s.folderClient.Exists(ctx, destFolderID)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
		}
		if !exists {
			return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", destFolderID)
		}
		updated, err := s.repo.SetFolderID(ctx, id, destFolderID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.Address(updated))
	})
	return &op, nil
}

// Если адреса нет вовсе — пробрасывается NotFound (через mapRepoErr).
// Если репо вернул другую ошибку — Internal.
func (s *AddressService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}

	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if existing.DeletionProtection {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has deletion_protection enabled; clear it via Update before Delete", id)
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete address %s", id),
		&vpcv1.DeleteAddressMetadata{AddressId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if err := s.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}

// AllocateResult — результат allocate-операций.
type AllocateResult struct {
	IP               string
	PoolID           string // только для external; "" для internal
	AlreadyAllocated bool
}

const (
	// allocateMaxAttempts — максимум попыток random-pick + retry-on-conflict.
	// При near-full CIDR (≥95% занято) random-pick имеет high false-fail rate
	// (см. allocateRandomPhase ниже). После этого порога переключаемся на
	// deterministic sweep.
	allocateMaxAttempts = 32

	// allocateRandomPhase — сколько попыток сделать random-pick'ом до того
	// как переключиться на deterministic sweep по тем же CIDR. Random в первые
	// N попыток дешевле (1 SQL/попытка), при low/medium occupancy сходится
	// быстро. Переход в sweep гарантирует closure под high-occupancy.
	allocateRandomPhase = 8
)

// AllocateInternalIP — выделяет next-free IPv4 в subnet, который указан
// в address.internal_ipv4.subnet_id. Idempotent: если IP уже выделен —
// возвращает existing с AlreadyAllocated=true.
//
// Iterate по ВСЕМ V4CidrBlocks subnet'а (раньше брал только [0] — если
// subnet расширили через AddCidrBlocks, второй+ prefix игнорировался и
// первая полная CIDR давала "exhausted"). Двухфазный allocator: random
// pick + deterministic sweep с tried-set (симметрично AllocateExternalIP),
// устраняет false-fail на near-full subnet (см. concurrency P0 #2).
func (s *AddressService) AllocateInternalIP(ctx context.Context, addressID string) (*AllocateResult, error) {
	addr, err := s.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.InternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no internal_ipv4 spec", addressID)
	}
	if addr.InternalIpv4.Address != "" {
		return &AllocateResult{IP: addr.InternalIpv4.Address, AlreadyAllocated: true}, nil
	}
	if addr.InternalIpv4.SubnetID == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s internal_ipv4.subnet_id is empty", addressID)
	}
	sub, err := s.subnetRepo.Get(ctx, addr.InternalIpv4.SubnetID)
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
			updated, err := s.repo.SetIPSpec(ctx, addressID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error",
					"subnet_id", sub.ID, "address_id", addressID, "ip_attempt", ip, "err", err)
				return nil, err
			}
			return &AllocateResult{IP: updated.InternalIpv4.Address}, nil
		}
		// Phase 2: deterministic sweep.
		for _, candidate := range usableIPv4Sweep(cidr, allocateMaxAttempts-allocateRandomPhase) {
			if _, dup := tried[candidate]; dup {
				continue
			}
			tried[candidate] = struct{}{}
			addr.InternalIpv4.Address = candidate
			updated, err := s.repo.SetIPSpec(ctx, addressID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error in sweep",
					"subnet_id", sub.ID, "address_id", addressID, "ip_attempt", candidate, "err", err)
				return nil, err
			}
			return &AllocateResult{IP: updated.InternalIpv4.Address}, nil
		}
	}
	slog.WarnContext(ctx, "allocator: subnet exhausted",
		"subnet_id", sub.ID,
		"address_id", addressID,
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

// v6AllocateMaxAttempts — число попыток random-pick + retry-on-conflict для
// AllocateInternalIPv6. IPv6-подсети огромные (обычно /64), коллизии редки —
// небольшого числа попыток достаточно.
const v6AllocateMaxAttempts = 16

// AllocateInternalIPv6 — выделяет случайный свободный IPv6 внутри
// subnet.v6_cidr_blocks[0] для Address с заполненным internal_ipv6.subnet_id.
// Idempotent: если адрес уже выделен — возвращает existing с AlreadyAllocated.
//
// Алгоритм: IPv6-подсети слишком большие для sweep — берём случайные host-биты
// внутри префикса, INSERT через SetInternalIPv6 + retry на UNIQUE-violation
// (constraint addresses_internal_subnet_ipv6_uniq). Пропускаем all-zeros
// (subnet-router anycast `<prefix>::`); broadcast в IPv6 нет.
func (s *AddressService) AllocateInternalIPv6(ctx context.Context, addressID string) (*AllocateResult, error) {
	addr, err := s.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.InternalIpv6 == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "address %s has no internal_ipv6 spec", addressID)
	}
	if addr.InternalIpv6.Address != "" {
		return &AllocateResult{IP: addr.InternalIpv6.Address, AlreadyAllocated: true}, nil
	}
	if addr.InternalIpv6.SubnetID == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "address %s internal_ipv6.subnet_id is empty", addressID)
	}
	sub, err := s.subnetRepo.Get(ctx, addr.InternalIpv6.SubnetID)
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
		updated, uerr := s.repo.SetInternalIPv6(ctx, addressID, addr.InternalIpv6)
		if uerr != nil {
			if isUniqueViolation(uerr) {
				conflicts++
				addr.InternalIpv6.Address = ""
				continue
			}
			slog.ErrorContext(ctx, "v6 allocator: SetInternalIPv6 returned non-conflict error",
				"subnet_id", sub.ID, "address_id", addressID, "ip_attempt", ip, "err", uerr)
			return nil, uerr
		}
		return &AllocateResult{IP: updated.InternalIpv6.Address}, nil
	}
	slog.WarnContext(ctx, "v6 allocator: exhausted attempts",
		"subnet_id", sub.ID, "address_id", addressID, "cidr", prefix.String(), "conflicts", conflicts)
	return nil, status.Errorf(codes.ResourceExhausted,
		"subnet %s: could not allocate a free IPv6 in %s after %d attempts (%d unique-conflicts)",
		sub.ID, prefix, v6AllocateMaxAttempts, conflicts)
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
		// /128 — единственный адрес.
		return addr.String(), nil
	}
	var rnd [16]byte
	for try := 0; try < 8; try++ {
		if _, err := rand.Read(rnd[:]); err != nil {
			return "", err
		}
		out := base
		// Накладываем случайные биты только на host-часть (последние hostBits бит).
		for i := 0; i < 16; i++ {
			bitIndex := i * 8 // первый бит байта i — это глобальный bit-index
			// сколько бит этого байта попадают в host-часть
			if bitIndex+8 <= bits {
				continue // целиком в network-части
			}
			var mask byte
			if bitIndex >= bits {
				mask = 0xff
			} else {
				keep := bits - bitIndex // старшие keep бит — network
				mask = byte(0xff >> keep)
			}
			out[i] = (base[i] &^ mask) | (rnd[i] & mask)
		}
		cand := netip.AddrFrom16(out)
		if cand == addr {
			continue // all-zeros host — subnet-router anycast, пропускаем
		}
		return cand.String(), nil
	}
	// Не смогли получить ненулевой host (крайне узкий префикс) — отдаём что есть.
	return addr.String(), nil
}

// AllocateExternalIP — резолвит pool через cascade и выделяет next-free IPv4
// из его cidr_blocks. Idempotent.
func (s *AddressService) AllocateExternalIP(ctx context.Context, addressID string) (*AllocateResult, error) {
	addr, err := s.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.ExternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no external_ipv4 spec", addressID)
	}
	if addr.ExternalIpv4.Address != "" {
		return &AllocateResult{
			IP:               addr.ExternalIpv4.Address,
			PoolID:           addr.ExternalIpv4.AddressPoolID,
			AlreadyAllocated: true,
		}, nil
	}

	// ResolvePoolForAddressObj переиспользует уже-полученный addr — устраняет
	// double-Get в request-path (cascade resolve внутри иначе делает повторный
	// addrRepo.Get для того же id).
	resolved, err := s.pools.ResolvePoolForAddressObj(ctx, addr)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
	}
	pool := resolved.Pool
	if len(pool.CIDRBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address pool %s has no cidr_blocks", pool.ID)
	}

	// Двухфазный allocate по каждому CIDR:
	//  Phase 1 (allocateRandomPhase попыток) — random-pick. Дёшево, сходится
	//          быстро при low/medium occupancy. Без shared-state между попытками.
	//  Phase 2 (deterministic sweep) — линейный обход CIDR с локальным
	//          tried-set для memoization. Гарантирует closure под high-occupancy
	//          (concurrency P0 #2 closure: устраняет ~9% false-fail на /28
	//          при 95%+ occupancy).
	//
	// Diagnostic: считаем сколько CIDR'ов реально доходит до allocate-loop'а;
	// если 0 — это invalid pool config (IPv6, parse fail), и FailedPrecondition
	// даёт оператору точную причину вместо вводящего в заблуждение «exhausted».
	parsedV4Count := 0
	totalConflicts := 0
	skippedNonV4 := 0
	parseFails := 0
	for _, cidrStr := range pool.CIDRBlocks {
		cidr, err := netip.ParsePrefix(strings.TrimSpace(cidrStr))
		if err != nil {
			parseFails++
			slog.WarnContext(ctx, "allocator: skipping unparseable cidr",
				"pool_id", pool.ID, "cidr", cidrStr, "err", err)
			continue
		}
		if !cidr.Addr().Is4() {
			skippedNonV4++
			slog.WarnContext(ctx, "allocator: skipping non-IPv4 cidr",
				"pool_id", pool.ID, "cidr", cidrStr)
			continue
		}
		parsedV4Count++
		tried := make(map[string]struct{}, allocateMaxAttempts)
		// Phase 1: random pick.
		for attempt := 0; attempt < allocateRandomPhase; attempt++ {
			ip, err := pickRandomIPv4(cidr)
			if err != nil {
				break // CIDR too small; try next
			}
			if _, dup := tried[ip]; dup {
				continue
			}
			tried[ip] = struct{}{}
			addr.ExternalIpv4.Address = ip
			addr.ExternalIpv4.AddressPoolID = pool.ID
			updated, err := s.repo.SetIPSpec(ctx, addressID, addr.ExternalIpv4, nil)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.ExternalIpv4.Address = ""
					addr.ExternalIpv4.AddressPoolID = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error",
					"pool_id", pool.ID, "address_id", addressID, "ip_attempt", ip, "err", err)
				return nil, err
			}
			return &AllocateResult{
				IP:     updated.ExternalIpv4.Address,
				PoolID: pool.ID,
			}, nil
		}
		// Phase 2: deterministic sweep — гарантия что мы перепробуем все
		// usable IPs в CIDR (с учётом tried-set).
		for _, candidate := range usableIPv4Sweep(cidr, allocateMaxAttempts-allocateRandomPhase) {
			if _, dup := tried[candidate]; dup {
				continue
			}
			tried[candidate] = struct{}{}
			addr.ExternalIpv4.Address = candidate
			addr.ExternalIpv4.AddressPoolID = pool.ID
			updated, err := s.repo.SetIPSpec(ctx, addressID, addr.ExternalIpv4, nil)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.ExternalIpv4.Address = ""
					addr.ExternalIpv4.AddressPoolID = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error in sweep",
					"pool_id", pool.ID, "address_id", addressID, "ip_attempt", candidate, "err", err)
				return nil, err
			}
			return &AllocateResult{
				IP:     updated.ExternalIpv4.Address,
				PoolID: pool.ID,
			}, nil
		}
	}
	slog.WarnContext(ctx, "allocator: exhausted",
		"pool_id", pool.ID,
		"address_id", addressID,
		"cidr_blocks", pool.CIDRBlocks,
		"parsed_ipv4", parsedV4Count,
		"skipped_non_v4", skippedNonV4,
		"parse_fails", parseFails,
		"unique_conflicts", totalConflicts)
	if parsedV4Count == 0 {
		// Pool без usable IPv4 CIDR — invalid config, не actually "exhausted".
		// Validation в Create/Update должна это ловить, но защищаемся для
		// legacy-pools где validation добавлена позже их создания.
		return nil, status.Errorf(codes.FailedPrecondition,
			"address pool %s has no IPv4 cidr_blocks (allocator requires IPv4)", pool.ID)
	}
	return nil, status.Errorf(codes.ResourceExhausted,
		"address pool %s exhausted (tried %d random + %d sweep IPs across %d cidr_blocks)",
		pool.ID, allocateRandomPhase, allocateMaxAttempts-allocateRandomPhase, parsedV4Count)
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
	// Skip network/broadcast для /≤30; для /31 оба usable; для /32 один.
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
//     Раньше offset считался как `rand%2 + 1` → возвращал base+1 или base+2,
//     второй вариант ВЫХОДИЛ за CIDR (UNIQUE-constraint в БД не валидирует
//     CIDR-membership → IP реально аллокировался снаружи pool.cidr).
//   - /≤30 (hostBits≥2): пропускаем .0 (network) и .last (broadcast) →
//     offset в [1, maxHosts].
func pickRandomIPv4(cidr netip.Prefix) (string, error) {
	if !cidr.Addr().Is4() {
		return "", ErrInvalidIPv4
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	var offset uint32
	switch hostBits {
	case 0:
		// /32 — единственный адрес.
		return cidr.Addr().String(), nil
	case 1:
		// /31 — оба валидны: offset ∈ {0, 1}.
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return "", err
		}
		offset = binary.BigEndian.Uint32(randBytes[:]) % 2
	default:
		// /≤30 — пропускаем network/broadcast: offset ∈ [1, 2^hostBits - 2].
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

// isUniqueViolation распознаёт UNIQUE-violation для retry-loop в allocate.
//
// Принципиальный путь: repo через wrapPgErr оборачивает SQLSTATE 23505 в
// ErrAlreadyExists — это и есть contract repo↔service. Substring-fallback
// оставлен для случаев когда какой-то новый repo может вернуть raw pgErr
// без обёртки (defensive). Constraint-specific имена удалены — service не
// должен знать DB-schema.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrAlreadyExists) {
		return true
	}
	// Defensive fallback: общие признаки UNIQUE-violation без leak'а
	// constraint-имён в service-layer.
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") ||
		strings.Contains(msg, "duplicate key value")
}
