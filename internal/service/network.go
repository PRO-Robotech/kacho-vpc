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
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// CreateNetworkReq — запрос на создание сети.
type CreateNetworkReq struct {
	FolderID    string
	Name        string
	Description string
	Labels      map[string]string
}

// UpdateNetworkReq — запрос на обновление сети.
type UpdateNetworkReq struct {
	NetworkID   string
	Name        string
	Description string
	Labels      map[string]string
	UpdateMask  []string
}

// NetworkService — бизнес-логика управления сетями.
type NetworkService struct {
	repo         NetworkRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewNetworkService создаёт NetworkService.
func NewNetworkService(repo NetworkRepo, folderClient FolderClient, opsRepo operations.Repo) *NetworkService {
	return &NetworkService{repo: repo, folderClient: folderClient, opsRepo: opsRepo}
}

// Get возвращает Network по ID.
func (s *NetworkService) Get(ctx context.Context, id string) (*domain.Network, error) {
	n, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return n, nil
}

// List возвращает список сетей в folder с пагинацией.
func (s *NetworkService) List(ctx context.Context, f NetworkFilter, p Pagination) ([]*domain.Network, string, error) {
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание Network, возвращает Operation.
func (s *NetworkService) Create(ctx context.Context, req CreateNetworkReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name required")
	}

	netID := ids.NewUID()
	op, err := operations.New(
		fmt.Sprintf("Create network %s", req.Name),
		&vpcv1.CreateNetworkMetadata{NetworkId: netID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, netID, req)
	})

	return &op, nil
}

func (s *NetworkService) doCreate(ctx context.Context, netID string, req CreateNetworkReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "folder %s not found", req.FolderID)
	}

	n := &domain.Network{
		ID:          netID,
		FolderID:    req.FolderID,
		CreatedAt:   time.Now().UTC(),
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
	}
	created, err := s.repo.Insert(ctx, n)
	if err != nil {
		return nil, err
	}
	return anypb.New(domainNetworkToProto(created))
}

// Update обновляет Network.
func (s *NetworkService) Update(ctx context.Context, req UpdateNetworkReq) (*operations.Operation, error) {
	if req.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}

	op, err := operations.New(
		fmt.Sprintf("Update network %s", req.NetworkID),
		&vpcv1.UpdateNetworkMetadata{NetworkId: req.NetworkID},
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

func (s *NetworkService) doUpdate(ctx context.Context, req UpdateNetworkReq) (*anypb.Any, error) {
	n, err := s.repo.Get(ctx, req.NetworkID)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	applyNetworkMask(n, req)

	updated, err := s.repo.Update(ctx, n)
	if err != nil {
		return nil, err
	}
	return anypb.New(domainNetworkToProto(updated))
}

func applyNetworkMask(n *domain.Network, req UpdateNetworkReq) {
	if len(req.UpdateMask) == 0 {
		// полное обновление
		n.Name = req.Name
		n.Description = req.Description
		n.Labels = req.Labels
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			n.Name = req.Name
		case "description":
			n.Description = req.Description
		case "labels":
			n.Labels = req.Labels
		}
	}
}

// Delete удаляет Network.
func (s *NetworkService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}

	op, err := operations.New(
		fmt.Sprintf("Delete network %s", id),
		&vpcv1.DeleteNetworkMetadata{NetworkId: id},
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
		return anypb.New(&vpcv1.DeleteNetworkMetadata{NetworkId: id})
	})

	return &op, nil
}

// domainNetworkToProto конвертирует domain Network в proto Network.
// Используется в worker-goroutines для формирования Operation.Response.
func domainNetworkToProto(n *domain.Network) *vpcv1.Network {
	return &vpcv1.Network{
		Id:                     n.ID,
		FolderId:               n.FolderID,
		Name:                   n.Name,
		Description:            n.Description,
		Labels:                 n.Labels,
		DefaultSecurityGroupId: n.DefaultSecurityGroupID,
	}
}

// mapRepoErr переводит domain-ошибки репозитория в gRPC-статусы.
func mapRepoErr(err error) error {
	if err == ErrNotFound {
		return status.Error(codes.NotFound, err.Error())
	}
	if err == ErrAlreadyExists {
		return status.Error(codes.AlreadyExists, err.Error())
	}
	return err
}
