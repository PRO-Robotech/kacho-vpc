package service

import (
	"context"
	"errors"
	"fmt"
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

// CreateSubnetReq — запрос на создание подсети.
type CreateSubnetReq struct {
	FolderID     string
	Name         string
	Description  string
	Labels       map[string]string
	NetworkID    string
	ZoneID       string
	V4CidrBlocks []string
	V6CidrBlocks []string
	RouteTableID string
	DhcpOptions  *domain.DhcpOptions
}

// UpdateSubnetReq — запрос на обновление подсети.
type UpdateSubnetReq struct {
	SubnetID     string
	Name         string
	Description  string
	Labels       map[string]string
	RouteTableID string
	DhcpOptions  *domain.DhcpOptions
	V4CidrBlocks []string
	UpdateMask   []string
}

// SubnetService — бизнес-логика управления подсетями.
type SubnetService struct {
	repo         SubnetRepo
	networkRepo  NetworkRepo
	folderClient FolderClient
	opsRepo      operations.Repo
	zoneReg      ZoneRegistry
	// addrRefRepo — опционально (wired через SetAddressRefRepo в cmd/vpc/main.go):
	// используется только для обогащения ListUsedAddresses записями referrer'ов
	// (кто использует адрес). nil → references[] пуст (graceful degradation).
	addrRefRepo AddressRepo
}

// NewSubnetService создаёт SubnetService.
//
// zoneReg — port для валидации `zone_id` через таблицу `zones` (источник
// истины вместо удалённого hardcode `ru-central1-{a,b,c,d}` в corelib).
func NewSubnetService(repo SubnetRepo, networkRepo NetworkRepo, folderClient FolderClient, opsRepo operations.Repo, zoneReg ZoneRegistry) *SubnetService {
	return &SubnetService{repo: repo, networkRepo: networkRepo, folderClient: folderClient, opsRepo: opsRepo, zoneReg: zoneReg}
}

// SetAddressRefRepo инъектирует AddressRepo для обогащения ListUsedAddresses
// записями referrer'ов (кто использует адрес). Вызывается из composition-root.
func (s *SubnetService) SetAddressRefRepo(r AddressRepo) { s.addrRefRepo = r }

// validateZoneID — sync-валидация zone_id: required + existence в БД.
//
// Возвращает gRPC InvalidArgument с FieldViolation для пустого значения
// (`<field> is required`); для несуществующей зоны — flat-message
// `unknown zone id '<zoneId>'` (verbatim YC, probe 2026-05-11; kacho-vpc#8).
// Любая другая ошибка БД → mapRepoErr.
func (s *SubnetService) validateZoneID(ctx context.Context, field, zoneID string) error {
	if err := corevalidate.ZoneId(field, zoneID); err != nil {
		return err
	}
	if s.zoneReg == nil {
		return nil // безопасный fallback для тестов без zoneReg
	}
	_, err := s.zoneReg.Get(ctx, zoneID)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return status.Errorf(codes.InvalidArgument, "unknown zone id '%s'", zoneID)
	}
	return mapRepoErr(err)
}

// Get возвращает Subnet по ID.
func (s *SubnetService) Get(ctx context.Context, id string) (*domain.Subnet, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	sub, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return sub, nil
}

// List возвращает список подсетей.
// folder_id обязателен (R10 #C1 closure).
func (s *SubnetService) List(ctx context.Context, f SubnetFilter, p Pagination) ([]*domain.Subnet, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание Subnet.
func (s *SubnetService) Create(ctx context.Context, req CreateSubnetReq) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, req.NetworkID); err != nil {
		return nil, err
	}
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// VPC Subnet принимает empty name (verbatim YC permissive policy для VPC).
	// folder_id / network_id больше НЕ валидируются sync — async через
	// folderClient.Exists / networkRepo.Get → NotFound (verbatim YC). См.
	// YC-DIFF-INVALID-PARENT-CODE.md, YC-DIFF-NAME-VALIDATION.md.
	// ZoneId: required + existence в таблице `zones` (без hardcoded whitelist;
	// допустимые значения формируются динамически из БД).
	if err := s.validateZoneID(ctx, "zone_id", req.ZoneID); err != nil {
		return nil, err
	}
	// Proto contract: v4_cidr_blocks больше НЕ (required) — подсеть может быть
	// создана без IPv4-диапазона (kacho-proto#8). Пустой список — легален; CIDR'ы,
	// которые ПЕРЕДАНЫ, всё ещё валидируются (host-bits=0, /16../28, disjointness),
	// а реальный диапазон добавляется позже через AddCidrBlocks.
	// SU-CIDR-2: host-bits в v4CidrBlocks (например `10.0.0.5/24`) → InvalidArgument.
	// Плюс ограничение размера префикса /28 (verbatim YC, kacho-vpc#10).
	for i, c := range req.V4CidrBlocks {
		if err := validateSubnetV4CIDR(fmt.Sprintf("v4_cidr_blocks[%d]", i), c); err != nil {
			return nil, err
		}
	}
	// v6_cidr_blocks — опциональны; если переданы, валидируем как IPv6 CIDR
	// в каноничной форме (host-bits=0). Immutable после Create (как v4).
	for i, c := range req.V6CidrBlocks {
		if err := validateSubnetV6CIDR(fmt.Sprintf("v6_cidr_blocks[%d]", i), c); err != nil {
			return nil, err
		}
	}
	if err := corevalidate.NameVPC("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}
	if err := validateDhcpOptions(req.DhcpOptions); err != nil {
		return nil, err
	}

	// Verbatim YC: existence / uniqueness / overlap checks run synchronously,
	// BEFORE the Operation. The async copies in doCreate + the DB EXCLUDE
	// constraint stay as the atomic backstops. См. kacho-vpc#8.
	if err := checkFolderExists(ctx, s.folderClient, req.FolderID); err != nil {
		return nil, err
	}
	if _, err := s.networkRepo.Get(ctx, req.NetworkID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", req.NetworkID)
		}
		return nil, mapRepoErr(err)
	}
	if req.Name != "" {
		existing, _, lerr := s.repo.List(ctx, SubnetFilter{FolderID: req.FolderID, Name: req.Name}, Pagination{})
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "Subnet with name %s already exists", req.Name)
		}
	}
	if err := s.checkSubnetCIDROverlap(ctx, req.FolderID, req.NetworkID, req.V4CidrBlocks); err != nil {
		return nil, err
	}

	subID := ids.NewID(ids.PrefixSubnet)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create subnet %s", req.Name),
		&vpcv1.CreateSubnetMetadata{SubnetId: subID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, subID, req)
	})

	return &op, nil
}

func (s *SubnetService) doCreate(ctx context.Context, subID string, req CreateSubnetReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		// verbatim YC text: "Folder with id <X> not found".
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
	}

	if _, err := s.networkRepo.Get(ctx, req.NetworkID); err != nil {
		// verbatim YC text: "Network <X> not found".
		return nil, status.Errorf(codes.NotFound, "Network %s not found", req.NetworkID)
	}

	// SU-CIDR-OVERLAP — пересечения v4 CIDR в рамках одной VPC ловятся
	// атомарно DB-level EXCLUDE constraint (миграция 0007), repo маппит
	// SQLSTATE 23P01 на ErrInvalidArg. См. SU-CIDR-OVERLAP.md.

	sub := &domain.Subnet{
		ID:           subID,
		FolderID:     req.FolderID,
		CreatedAt:    time.Now().UTC(),
		Name:         req.Name,
		Description:  req.Description,
		Labels:       req.Labels,
		NetworkID:    req.NetworkID,
		ZoneID:       req.ZoneID,
		V4CidrBlocks: req.V4CidrBlocks,
		V6CidrBlocks: req.V6CidrBlocks,
		RouteTableID: req.RouteTableID,
		DhcpOptions:  req.DhcpOptions,
	}
	created, err := s.repo.Insert(ctx, sub)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(protoconv.Subnet(created))
}

// Update обновляет Subnet.
//
// SU-CIDR-IM-1: network_id / zone_id — hard-immutable: явное указание в
// update_mask → InvalidArgument; присланное в body без mask — silently
// игнорируется (full-object PATCH UI). v4_cidr_blocks / v6_cidr_blocks —
// verbatim YC (probe 2026-05-11, kacho-vpc#10) НЕ отвергает их в mask: YC
// принимает запрос (200). Мы тоже принимаем — но репозиторный Update не
// перезаписывает CIDR-колонки (defensive depth), т.е. изменение CIDR через
// Update — no-op (документировано в 07-known-divergences.md).
func (s *SubnetService) Update(ctx context.Context, req UpdateSubnetReq) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, req.SubnetID); err != nil {
		return nil, err
	}
	if req.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "network_id", "zone_id":
			return nil, invalidArg(field, field+" is immutable after Subnet.Create")
		}
	}
	if err := validateSubnetUpdate(req); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update subnet %s", req.SubnetID),
		&vpcv1.UpdateSubnetMetadata{SubnetId: req.SubnetID},
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

func (s *SubnetService) doUpdate(ctx context.Context, req UpdateSubnetReq) (*anypb.Any, error) {
	sub, err := s.repo.Get(ctx, req.SubnetID)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	applySubnetMask(sub, req)

	updated, err := s.repo.Update(ctx, sub)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(protoconv.Subnet(updated))
}

// validateSubnetUpdate проверяет name/description/labels в Update.
//
// Поля immutable (v4_cidr_blocks, network_id, zone_id) обрабатываются ВЫШЕ:
// в Update; здесь они уже отсеяны.
func validateSubnetUpdate(req UpdateSubnetReq) error {
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {},
		"route_table_id": {}, "dhcp_options": {},
		// immutable-поля разрешены в known только чтобы пройти UpdateMask-check;
		// сама immutability ловится выше.
		"v4_cidr_blocks": {}, "v6_cidr_blocks": {}, "network_id": {}, "zone_id": {},
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
			// VPC Subnet: empty name allowed (YC permissive policy).
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
		case "dhcp_options":
			if err := validateDhcpOptions(req.DhcpOptions); err != nil {
				return err
			}
		}
	}
	// Полный апдейт (без update_mask) — DhcpOptions тоже валидируются.
	if len(req.UpdateMask) == 0 {
		if err := validateDhcpOptions(req.DhcpOptions); err != nil {
			return err
		}
	}
	return nil
}

// validateDhcpOptions — verbatim YC contract:
//   - domainName: RFC 1123 DNS name либо empty.
//   - domainNameServers[]: каждый элемент — IP-адрес.
//   - ntpServers[]: каждый элемент — IP-адрес.
//
// Probe YC (2026-05-04):
//   - "!!!" → 400 "Illegal argument Invalid domain name '!!!'"
//   - "not-an-ip" в domainNameServers → 400 "Cannot parse address: not-an-ip"
//   - "pool.ntp.org" в ntpServers → 400 "Cannot parse address: pool.ntp.org"
func validateDhcpOptions(d *domain.DhcpOptions) error {
	if d == nil {
		return nil
	}
	if err := corevalidate.DhcpDomainName("dhcp_options.domain_name", d.DomainName); err != nil {
		return err
	}
	for _, ns := range d.DomainNameServers {
		if err := corevalidate.IPAddress("dhcp_options.domain_name_servers", ns); err != nil {
			return err
		}
	}
	for _, ntp := range d.NtpServers {
		if err := corevalidate.IPAddress("dhcp_options.ntp_servers", ntp); err != nil {
			return err
		}
	}
	return nil
}

// applySubnetMask применяет mutable поля из req к sub.
//
// Immutable fields (v4_cidr_blocks, v6_cidr_blocks, network_id, zone_id) НЕ
// применяются никогда — даже если клиент прислал их в body без mask. Sync-check
// в Update() уже отверг бы попытку явно указать их в update_mask.
func applySubnetMask(sub *domain.Subnet, req UpdateSubnetReq) {
	if len(req.UpdateMask) == 0 {
		// Полный update — только mutable fields.
		sub.Name = req.Name
		sub.Description = req.Description
		sub.Labels = req.Labels
		sub.RouteTableID = req.RouteTableID
		sub.DhcpOptions = req.DhcpOptions
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			sub.Name = req.Name
		case "description":
			sub.Description = req.Description
		case "labels":
			sub.Labels = req.Labels
		case "route_table_id":
			sub.RouteTableID = req.RouteTableID
		case "dhcp_options":
			sub.DhcpOptions = req.DhcpOptions
		}
	}
}

// ListOperations возвращает операции для конкретного Subnet (фильтр resource_id).
func (s *SubnetService) ListOperations(ctx context.Context, subnetID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, subnetID); err != nil {
		return nil, "", err
	}
	if _, err := s.repo.Get(ctx, subnetID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: subnetID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}

// Move инициирует перенос Subnet в другой folder.
func (s *SubnetService) Move(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
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
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Move subnet %s", id),
		&vpcv1.MoveSubnetMetadata{SubnetId: id})
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
		return anypb.New(protoconv.Subnet(updated))
	})
	return &op, nil
}

// AddCidrBlocks добавляет CIDR-блоки в подсеть атомарно.
//
// YC verbatim: возвращает Operation; внутри worker'а:
//   - Get subnet → если не найден → NotFound.
//   - Validate каждого CIDR (host-bits=0).
//   - Проверка overlap внутри новой объединённой коллекции.
//   - SetCidrBlocks (DB UPDATE). EXCLUDE constraint subnets_no_overlap_v4
//     проверяет primary CIDR на overlap с другими подсетями этой сети.
//
// Известное ограничение: EXCLUDE checks только array[1]. Если v4_cidr_primary
// неизменен (т.е. добавляем не в начало), overlap с соседними подсетями по
// добавляемым CIDR не проверяется на DB-уровне. Покрываем service-level
// проверкой через networkRepo / List.
func (s *SubnetService) AddCidrBlocks(ctx context.Context, id string, v4 []string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if len(v4) == 0 {
		return nil, invalidArg("v4_cidr_blocks", "v4_cidr_blocks is required")
	}
	for i, c := range v4 {
		if err := validateSubnetV4CIDR(fmt.Sprintf("v4_cidr_blocks[%d]", i), c); err != nil {
			return nil, err
		}
	}

	op, err := operations.New(ids.PrefixOperationVPC,
		fmt.Sprintf("Add CIDR blocks to subnet %s", id),
		&vpcv1.UpdateSubnetMetadata{SubnetId: id})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		sub, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		merged := append([]string{}, sub.V4CidrBlocks...)
		merged = append(merged, v4...)
		// Проверка пересечений внутри объединённого набора (sync, host-bits уже OK).
		if err := checkCIDRDisjoint(merged); err != nil {
			return nil, err
		}
		updated, err := s.repo.SetCidrBlocks(ctx, id, merged)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.Subnet(updated))
	})
	return &op, nil
}

// RemoveCidrBlocks удаляет CIDR-блоки из подсети атомарно.
//
// YC verbatim:
//   - Если CIDR не присутствует → FailedPrecondition.
//   - Если будет удалён последний CIDR → FailedPrecondition (subnet не может быть пустой).
//   - Если внутри CIDR есть Address — на текущей фазе пропускаем (доп. проверка
//     потребует JSON-запрос по addresses; в будущем добавится).
func (s *SubnetService) RemoveCidrBlocks(ctx context.Context, id string, v4 []string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if len(v4) == 0 {
		return nil, invalidArg("v4_cidr_blocks", "v4_cidr_blocks is required")
	}
	op, err := operations.New(ids.PrefixOperationVPC,
		fmt.Sprintf("Remove CIDR blocks from subnet %s", id),
		&vpcv1.UpdateSubnetMetadata{SubnetId: id})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		sub, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		toRemove := map[string]struct{}{}
		for _, c := range v4 {
			toRemove[c] = struct{}{}
		}
		var remaining []string
		var removed int
		for _, existing := range sub.V4CidrBlocks {
			if _, ok := toRemove[existing]; ok {
				removed++
				continue
			}
			remaining = append(remaining, existing)
		}
		if removed != len(v4) {
			return nil, status.Errorf(codes.FailedPrecondition, "one or more CIDR blocks not found in subnet")
		}
		if len(remaining) == 0 {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot remove last CIDR block from subnet")
		}
		updated, err := s.repo.SetCidrBlocks(ctx, id, remaining)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.Subnet(updated))
	})
	return &op, nil
}

// Relocate переносит подсеть в другую zone.
//
// Verbatim YC (probe 2026-05-11, kacho-vpc#10): Relocate ВСЕГДА отвергается
// синхронно с FailedPrecondition "Invalid subnet state" — даже для свежей
// подсети без адресов и валидной целевой зоны. YC требует какое-то внутреннее
// состояние подсети, которое control-plane без data-plane не моделирует
// (multi-zone network?). Поэтому Operation не создаётся: после format-check
// id, валидации destination_zone_id и проверки существования подсети →
// FAILED_PRECONDITION "Invalid subnet state".
func (s *SubnetService) Relocate(ctx context.Context, id, destZoneID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if err := s.validateZoneID(ctx, "destination_zone_id", destZoneID); err != nil {
		return nil, err
	}
	if _, err := s.repo.Get(ctx, id); err != nil {
		return nil, mapRepoErr(err)
	}
	return nil, status.Error(codes.FailedPrecondition, "Invalid subnet state")
}

// ListUsedAddresses возвращает Address-ресурсы, привязанные к подсети
// (через internal_ipv4.subnet_id) + referrer-записи (кто использует адрес,
// map address-id → reference; ключ отсутствует если referrer'а нет).
// Sync RPC, не Operation.
func (s *SubnetService) ListUsedAddresses(ctx context.Context, subnetID string, p Pagination) ([]*domain.Address, map[string]*domain.AddressReference, string, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, subnetID); err != nil {
		return nil, nil, "", err
	}
	if subnetID == "" {
		return nil, nil, "", status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if _, err := s.repo.Get(ctx, subnetID); err != nil {
		return nil, nil, "", mapRepoErr(err)
	}
	addrs, nextToken, err := s.repo.AddressesBySubnet(ctx, subnetID, p)
	if err != nil {
		return nil, nil, "", mapRepoErr(err)
	}
	refs := map[string]*domain.AddressReference{}
	if s.addrRefRepo != nil && len(addrs) > 0 {
		idsList := make([]string, 0, len(addrs))
		for _, a := range addrs {
			idsList = append(idsList, a.ID)
		}
		refs, err = s.addrRefRepo.ReferencesForAddresses(ctx, idsList)
		if err != nil {
			return nil, nil, "", mapRepoErr(err)
		}
	}
	return addrs, refs, nextToken, nil
}

// checkSubnetCIDROverlap — sync FAILED_PRECONDITION "Subnet CIDRs can not
// overlap" if any of the requested v4 CIDRs overlaps a CIDR of an existing
// subnet in the same network/folder. The DB EXCLUDE constraint (миграция 0007)
// stays as the atomic backstop in doCreate. См. kacho-vpc#8.
func (s *SubnetService) checkSubnetCIDROverlap(ctx context.Context, folderID, networkID string, v4 []string) error {
	if len(v4) == 0 {
		return nil
	}
	newPrefixes := make([]netipPrefix, 0, len(v4))
	for _, c := range v4 {
		pr, err := parseNetipPrefix(c)
		if err != nil {
			// host-bits / format already validated upstream; be defensive.
			return invalidArg("v4_cidr_blocks", "must be valid CIDR")
		}
		newPrefixes = append(newPrefixes, pr)
	}
	existing, _, err := s.repo.List(ctx, SubnetFilter{FolderID: folderID, NetworkID: networkID}, Pagination{})
	if err != nil {
		return mapRepoErr(err)
	}
	for _, sub := range existing {
		for _, raw := range sub.V4CidrBlocks {
			pr, perr := parseNetipPrefix(raw)
			if perr != nil {
				continue
			}
			for _, np := range newPrefixes {
				if prefixesOverlap(pr, np) {
					return status.Errorf(codes.FailedPrecondition, "Subnet CIDRs can not overlap")
				}
			}
		}
	}
	return nil
}

// checkCIDRDisjoint — sync-проверка, что массив CIDR не содержит пересекающихся.
func checkCIDRDisjoint(cidrs []string) error {
	prefixes := make([]netipPrefix, 0, len(cidrs))
	for i, c := range cidrs {
		pr, err := parseNetipPrefix(c)
		if err != nil {
			return invalidArg(fmt.Sprintf("v4_cidr_blocks[%d]", i), "must be valid CIDR")
		}
		prefixes = append(prefixes, pr)
	}
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if prefixesOverlap(prefixes[i], prefixes[j]) {
				return status.Errorf(codes.FailedPrecondition, "Subnet CIDRs can not overlap")
			}
		}
	}
	return nil
}

// Delete удаляет Subnet.
func (s *SubnetService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	// Verbatim YC: a Subnet with internal Address children can not be deleted —
	// sync FAILED_PRECONDITION. The async FK RESTRICT path stays as the atomic
	// backstop in the worker. См. kacho-vpc#8.
	addrs, _, aerr := s.repo.AddressesBySubnet(ctx, id, Pagination{})
	if aerr != nil {
		return nil, mapRepoErr(aerr)
	}
	if len(addrs) > 0 {
		return nil, status.Error(codes.FailedPrecondition, "Subnet has allocated internal addresses")
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete subnet %s", id),
		&vpcv1.DeleteSubnetMetadata{SubnetId: id},
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
