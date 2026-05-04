package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
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
}

// NewSubnetService создаёт SubnetService.
func NewSubnetService(repo SubnetRepo, networkRepo NetworkRepo, folderClient FolderClient, opsRepo operations.Repo) *SubnetService {
	return &SubnetService{repo: repo, networkRepo: networkRepo, folderClient: folderClient, opsRepo: opsRepo}
}

// Get возвращает Subnet по ID.
func (s *SubnetService) Get(ctx context.Context, id string) (*domain.Subnet, error) {
	sub, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return sub, nil
}

// List возвращает список подсетей.
func (s *SubnetService) List(ctx context.Context, f SubnetFilter, p Pagination) ([]*domain.Subnet, string, error) {
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание Subnet.
func (s *SubnetService) Create(ctx context.Context, req CreateSubnetReq) (*operations.Operation, error) {
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
	// ZoneId — verbatim YC whitelist `ru-central1-{a,b,c,d}`. Пустой zone_id —
	// `zone_id is required`. См. ZONE-ID-VALIDATION.md.
	if err := corevalidate.ZoneId("zone_id", req.ZoneID); err != nil {
		return nil, err
	}
	// SU-CIDR-2: host-bits в v4CidrBlocks (например `10.0.0.5/24`) → InvalidArgument.
	for i, c := range req.V4CidrBlocks {
		if err := validateCIDRPrefix(fmt.Sprintf("v4_cidr_blocks[%d]", i), c); err != nil {
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

	subID := ids.NewUID()
	op, err := operations.New(
		"vpc",
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
		RouteTableID: req.RouteTableID,
		DhcpOptions:  req.DhcpOptions,
	}
	created, err := s.repo.Insert(ctx, sub)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(domainSubnetToProto(created))
}

// Update обновляет Subnet.
//
// SU-CIDR-IM-1: v4_cidr_blocks / v6_cidr_blocks / network_id / zone_id —
// immutable после Create. Любая попытка изменить (через update_mask или
// «полный апдейт» с непустым V4CidrBlocks) → InvalidArgument до Operation.
func (s *SubnetService) Update(ctx context.Context, req UpdateSubnetReq) (*operations.Operation, error) {
	if req.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "v4_cidr_blocks", "v6_cidr_blocks", "network_id", "zone_id":
			return nil, invalidArg(field, field+" is immutable after Subnet.Create")
		}
	}
	// Без update_mask клиент мог прислать V4CidrBlocks в полном-апдейте — так
	// тоже нельзя.
	if len(req.UpdateMask) == 0 && len(req.V4CidrBlocks) > 0 {
		return nil, invalidArg("v4_cidr_blocks", "v4_cidr_blocks is immutable after Subnet.Create")
	}
	if err := validateSubnetUpdate(req); err != nil {
		return nil, err
	}

	op, err := operations.New(
		"vpc",
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
		return nil, err
	}
	return anypb.New(domainSubnetToProto(updated))
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

func applySubnetMask(sub *domain.Subnet, req UpdateSubnetReq) {
	if len(req.UpdateMask) == 0 {
		sub.Name = req.Name
		sub.Description = req.Description
		sub.Labels = req.Labels
		sub.RouteTableID = req.RouteTableID
		sub.DhcpOptions = req.DhcpOptions
		if len(req.V4CidrBlocks) > 0 {
			sub.V4CidrBlocks = req.V4CidrBlocks
		}
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
		case "v4_cidr_blocks":
			sub.V4CidrBlocks = req.V4CidrBlocks
		}
	}
}

// Delete удаляет Subnet.
func (s *SubnetService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}

	op, err := operations.New(
		"vpc",
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
		return anypb.New(&vpcv1.DeleteSubnetMetadata{SubnetId: id})
	})

	return &op, nil
}

// domainSubnetToProto конвертирует domain Subnet в proto Subnet.
func domainSubnetToProto(s *domain.Subnet) *vpcv1.Subnet {
	p := &vpcv1.Subnet{
		Id:           s.ID,
		FolderId:     s.FolderID,
		Name:         s.Name,
		Description:  s.Description,
		Labels:       s.Labels,
		NetworkId:    s.NetworkID,
		ZoneId:       s.ZoneID,
		V4CidrBlocks: s.V4CidrBlocks,
		V6CidrBlocks: s.V6CidrBlocks,
		RouteTableId: s.RouteTableID,
	}
	if s.DhcpOptions != nil {
		p.DhcpOptions = &vpcv1.DhcpOptions{
			DomainNameServers: s.DhcpOptions.DomainNameServers,
			DomainName:        s.DhcpOptions.DomainName,
			NtpServers:        s.DhcpOptions.NtpServers,
		}
	}
	return p
}
