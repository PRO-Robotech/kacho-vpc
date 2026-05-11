package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	pe "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
)

// CreatePrivateEndpointReq — запрос на создание PrivateEndpoint.
type CreatePrivateEndpointReq struct {
	FolderID    string
	Name        string
	Description string
	Labels      map[string]string
	NetworkID   string
	SubnetID    string
	IPAddress   string
	AddressID   string
	ServiceType string
	DnsOptions  map[string]any
}

// UpdatePrivateEndpointReq — запрос на обновление PrivateEndpoint.
type UpdatePrivateEndpointReq struct {
	PrivateEndpointID string
	Name              string
	Description       string
	Labels            map[string]string
	DnsOptions        map[string]any
	UpdateMask        []string
}

// PrivateEndpointService — бизнес-логика управления PrivateEndpoints.
type PrivateEndpointService struct {
	repo         PrivateEndpointRepo
	folderClient FolderClient
	networkRepo  NetworkRepo
	subnetRepo   SubnetRepo
	opsRepo      operations.Repo
}

// NewPrivateEndpointService создаёт PrivateEndpointService.
func NewPrivateEndpointService(repo PrivateEndpointRepo, folderClient FolderClient, networkRepo NetworkRepo, subnetRepo SubnetRepo, opsRepo operations.Repo) *PrivateEndpointService {
	return &PrivateEndpointService{
		repo: repo, folderClient: folderClient,
		networkRepo: networkRepo, subnetRepo: subnetRepo,
		opsRepo: opsRepo,
	}
}

// Get возвращает PrivateEndpoint по ID.
func (s *PrivateEndpointService) Get(ctx context.Context, id string) (*domain.PrivateEndpoint, error) {
	got, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return got, nil
}

// List возвращает список PrivateEndpoints.
// folder_id обязателен (R10 #C1 closure).
func (s *PrivateEndpointService) List(ctx context.Context, f PrivateEndpointFilter, p Pagination) ([]*domain.PrivateEndpoint, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание PrivateEndpoint, возвращает Operation.
func (s *PrivateEndpointService) Create(ctx context.Context, req CreatePrivateEndpointReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
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

	peID := ids.NewID(ids.PrefixPrivateEndpoint)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create private endpoint %s", req.Name),
		&pe.CreatePrivateEndpointMetadata{PrivateEndpointId: peID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, peID, req)
	})
	return &op, nil
}

func (s *PrivateEndpointService) doCreate(ctx context.Context, peID string, req CreatePrivateEndpointReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
	}

	if _, err := s.networkRepo.Get(ctx, req.NetworkID); err != nil {
		return nil, status.Errorf(codes.NotFound, "Network %s not found", req.NetworkID)
	}
	if req.SubnetID != "" {
		if _, err := s.subnetRepo.Get(ctx, req.SubnetID); err != nil {
			return nil, status.Errorf(codes.NotFound, "Subnet %s not found", req.SubnetID)
		}
	}

	stype := req.ServiceType
	if stype == "" {
		stype = "object_storage"
	}

	p := &domain.PrivateEndpoint{
		ID:          peID,
		FolderID:    req.FolderID,
		CreatedAt:   time.Now().UTC(),
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		NetworkID:   req.NetworkID,
		SubnetID:    req.SubnetID,
		AddressID:   req.AddressID,
		IPAddress:   req.IPAddress,
		ServiceType: stype,
		DnsOptions:  req.DnsOptions,
		Status:      "AVAILABLE",
	}
	created, err := s.repo.Insert(ctx, p)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(protoconv.PrivateEndpoint(created))
}

// Update обновляет PrivateEndpoint.
func (s *PrivateEndpointService) Update(ctx context.Context, req UpdatePrivateEndpointReq) (*operations.Operation, error) {
	if req.PrivateEndpointID == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	if err := validatePrivateEndpointUpdate(req); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update private endpoint %s", req.PrivateEndpointID),
		&pe.UpdatePrivateEndpointMetadata{PrivateEndpointId: req.PrivateEndpointID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		got, err := s.repo.Get(ctx, req.PrivateEndpointID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		applyPrivateEndpointMask(got, req)
		updated, err := s.repo.Update(ctx, got)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.PrivateEndpoint(updated))
	})
	return &op, nil
}

func validatePrivateEndpointUpdate(req UpdatePrivateEndpointReq) error {
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}, "dns_options": {}}
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

func applyPrivateEndpointMask(p *domain.PrivateEndpoint, req UpdatePrivateEndpointReq) {
	if len(req.UpdateMask) == 0 {
		p.Name = req.Name
		p.Description = req.Description
		p.Labels = req.Labels
		if req.DnsOptions != nil {
			p.DnsOptions = req.DnsOptions
		}
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			p.Name = req.Name
		case "description":
			p.Description = req.Description
		case "labels":
			p.Labels = req.Labels
		case "dns_options":
			p.DnsOptions = req.DnsOptions
		}
	}
}

// Delete удаляет PrivateEndpoint.
func (s *PrivateEndpointService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete private endpoint %s", id),
		&pe.DeletePrivateEndpointMetadata{PrivateEndpointId: id},
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
		// proto-options: response = google.protobuf.Empty (verbatim YC).
		// Metadata уже передана при operations.New выше; в response — Empty.
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}

// ListOperations возвращает операции для PE.
func (s *PrivateEndpointService) ListOperations(ctx context.Context, peID string, p Pagination) ([]operations.Operation, string, error) {
	if _, err := s.repo.Get(ctx, peID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: peID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
