package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
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
	// Для internal (если InternalSpec != nil):
	InternalSpec *InternalAddrSpec
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

// AddressService — бизнес-логика управления IP-адресами.
//
// Сервис pure: НЕ генерирует IP, НЕ дёргает random/network. Если клиент
// не указал ip-адрес — поле остаётся пустым; внешний controller
// (`kacho-vpc-controllers`, allocator-loop) затем UPDATE-нёт address с
// аллоцированным IP.
//
// См. POST-PROCESSING-IN-CONTROLLERS.md (architecture-decision).
type AddressService struct {
	repo         AddressRepo
	subnetRepo   SubnetRepo
	folderClient FolderClient
	opsRepo      operations.Repo
	// allocator — inline IPAM (Phase-2 cleanup: kacho-vpc-controllers удалён).
	// Может быть nil в test-setup'ах (тогда IP остаётся пустым; legacy behaviour).
	allocator *AddressAllocator
}

// NewAddressService создаёт AddressService.
func NewAddressService(repo AddressRepo, subnetRepo SubnetRepo, folderClient FolderClient, opsRepo operations.Repo) *AddressService {
	return &AddressService{repo: repo, subnetRepo: subnetRepo, folderClient: folderClient, opsRepo: opsRepo}
}

// SetAllocator wires inline IPAM allocator. Должно вызываться composition root
// после конструирования AddressPoolService (циклическая зависимость
// AddressService ↔ AddressAllocator → AddressPoolService → ... → AddressRepo,
// поэтому через setter).
func (s *AddressService) SetAllocator(a *AddressAllocator) { s.allocator = a }

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
		return invalidArg(
			"internal_ipv4_address_spec.address",
			"subnet has no v4 cidr block; cannot validate explicit address",
		)
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
	return a, nil
}

// ListBySubnet возвращает Address-ы, привязанные к указанной подсети.
//
// Использует subnetRepo.AddressesBySubnet (joining через internal_ipv4.subnet_id).
func (s *AddressService) ListBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*domain.Address, string, error) {
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
	return addrs, nextToken, nil
}

// Get возвращает Address по ID.
func (s *AddressService) Get(ctx context.Context, id string) (*domain.Address, error) {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return a, nil
}

// List возвращает список адресов.
// folder_id обязателен (R10 #C1 closure).
func (s *AddressService) List(ctx context.Context, f AddressFilter, p Pagination) ([]*domain.Address, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание Address.
func (s *AddressService) Create(ctx context.Context, req CreateAddressReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.ExternalSpec == nil && req.InternalSpec == nil {
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

	// Sync-проверка: explicit IP (`internal_ipv4_address_spec.address`) должен
	// принадлежать CIDR-блоку указанной subnet. Если subnet не найден — sync
	// валидация пропускается (NotFound будет async, как и для остальных
	// FK — verbatim YC, см. YC-DIFF-INVALID-PARENT-CODE.md). Если IP вне CIDR
	// — возвращаем sync InvalidArgument: иначе адрес попадает в БД и пишется
	// в NetBox, минуя любые FK, что приводит к мусору в IPAM.
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
		// Pure: записываем то, что пришло от клиента; если address пустой —
		// его аллоцирует kacho-vpc-controllers (allocator-loop).
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
	} else {
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
	}

	created, err := s.repo.Insert(ctx, a)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	// Inline IPAM allocation (Phase-2: kacho-vpc-controllers упразднён).
	// Если client не передал explicit IP — выделяем здесь же в worker'е.
	// Idempotent: если IP уже выставлен — Allocate* возвращает существующий.
	if s.allocator != nil {
		if created.ExternalIpv4 != nil && created.ExternalIpv4.Address == "" {
			res, aerr := s.allocator.AllocateExternalIP(ctx, created.ID)
			if aerr != nil {
				// FailedPrecondition с "no external_ipv4 spec" не должен возникать
				// (мы только что записали spec). Любая ошибка allocate — log + проброс.
				return nil, status.Errorf(codes.FailedPrecondition,
					"allocate external ip: %v", aerr)
			}
			created.ExternalIpv4.Address = res.IP
			created.ExternalIpv4.AddressPoolID = res.PoolID
		}
		if created.InternalIpv4 != nil && created.InternalIpv4.Address == "" && created.InternalIpv4.SubnetID != "" {
			res, aerr := s.allocator.AllocateInternalIP(ctx, created.ID)
			if aerr != nil {
				return nil, status.Errorf(codes.FailedPrecondition,
					"allocate internal ip: %v", aerr)
			}
			created.InternalIpv4.Address = res.IP
		}
	}
	return anypb.New(domainAddressToProto(created))
}

// Update обновляет Address.
//
// Sync-валидация: см. validateAddressUpdate. Address — особый случай: name
// может быть пустым (как и в Create), потому что для адресов name необязательное.
func (s *AddressService) Update(ctx context.Context, req UpdateAddressReq) (*operations.Operation, error) {
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
	return anypb.New(domainAddressToProto(updated))
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
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
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
		return anypb.New(domainAddressToProto(updated))
	})
	return &op, nil
}

// Если адреса нет вовсе — пробрасывается NotFound (через mapRepoErr).
// Если репо вернул другую ошибку — Internal.
func (s *AddressService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
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

// domainAddressToProto конвертирует domain Address в proto Address.
func domainAddressToProto(a *domain.Address) *vpcv1.Address {
	p := &vpcv1.Address{
		Id:                 a.ID,
		FolderId:           a.FolderID,
		Name:               a.Name,
		Description:        a.Description,
		Labels:             a.Labels,
		Reserved:           a.Reserved,
		Used:               a.Used,
		Type:               vpcv1.Address_Type(a.Type),
		IpVersion:          vpcv1.Address_IpVersion(a.IpVersion),
		DeletionProtection: a.DeletionProtection,
	}
	if a.ExternalIpv4 != nil {
		ext := &vpcv1.ExternalIpv4Address{
			Address: a.ExternalIpv4.Address,
			ZoneId:  a.ExternalIpv4.ZoneID,
		}
		if a.ExternalIpv4.Requirements != nil {
			ext.Requirements = &vpcv1.AddressRequirements{
				DdosProtectionProvider: a.ExternalIpv4.Requirements.DdosProtectionProvider,
				OutgoingSmtpCapability: a.ExternalIpv4.Requirements.OutgoingSmtpCapability,
			}
		}
		p.Address = &vpcv1.Address_ExternalIpv4Address{ExternalIpv4Address: ext}
	} else if a.InternalIpv4 != nil {
		p.Address = &vpcv1.Address_InternalIpv4Address{
			InternalIpv4Address: &vpcv1.InternalIpv4Address{
				Address: a.InternalIpv4.Address,
				Scope: &vpcv1.InternalIpv4Address_SubnetId{
					SubnetId: a.InternalIpv4.SubnetID,
				},
			},
		}
	}
	return p
}
