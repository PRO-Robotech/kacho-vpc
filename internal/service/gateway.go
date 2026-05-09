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
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// CreateGatewayReq — запрос на создание NAT gateway.
type CreateGatewayReq struct {
	FolderID    string
	Name        string
	Description string
	Labels      map[string]string
	GatewayType string // "shared_egress" в текущем YC
}

// UpdateGatewayReq — запрос на обновление gateway.
type UpdateGatewayReq struct {
	GatewayID   string
	Name        string
	Description string
	Labels      map[string]string
	GatewayType string
	UpdateMask  []string
}

// GatewayService — бизнес-логика управления NAT gateways.
type GatewayService struct {
	repo         GatewayRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewGatewayService создаёт GatewayService.
func NewGatewayService(repo GatewayRepo, folderClient FolderClient, opsRepo operations.Repo) *GatewayService {
	return &GatewayService{repo: repo, folderClient: folderClient, opsRepo: opsRepo}
}

// Get возвращает Gateway по ID.
func (s *GatewayService) Get(ctx context.Context, id string) (*domain.Gateway, error) {
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return g, nil
}

// List возвращает список Gateways.
// folder_id обязателен (R10 #C1 closure).
func (s *GatewayService) List(ctx context.Context, f GatewayFilter, p Pagination) ([]*domain.Gateway, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание Gateway, возвращает Operation.
func (s *GatewayService) Create(ctx context.Context, req CreateGatewayReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if err := corevalidate.NameGateway("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}

	gwID := ids.NewID(ids.PrefixGateway)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create gateway %s", req.Name),
		&vpcv1.CreateGatewayMetadata{GatewayId: gwID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, gwID, req)
	})
	return &op, nil
}

func (s *GatewayService) doCreate(ctx context.Context, gwID string, req CreateGatewayReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
	}

	gtype := req.GatewayType
	if gtype == "" {
		gtype = "shared_egress"
	}

	g := &domain.Gateway{
		ID:          gwID,
		FolderID:    req.FolderID,
		CreatedAt:   time.Now().UTC(),
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		GatewayType: gtype,
	}
	created, err := s.repo.Insert(ctx, g)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(domainGatewayToProto(created))
}

// Update обновляет Gateway.
func (s *GatewayService) Update(ctx context.Context, req UpdateGatewayReq) (*operations.Operation, error) {
	if req.GatewayID == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	if err := validateGatewayUpdate(req); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update gateway %s", req.GatewayID),
		&vpcv1.UpdateGatewayMetadata{GatewayId: req.GatewayID},
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

func (s *GatewayService) doUpdate(ctx context.Context, req UpdateGatewayReq) (*anypb.Any, error) {
	g, err := s.repo.Get(ctx, req.GatewayID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applyGatewayMask(g, req)
	updated, err := s.repo.Update(ctx, g)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(domainGatewayToProto(updated))
}

func validateGatewayUpdate(req UpdateGatewayReq) error {
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}, "gateway_type": {}}
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
			if err := corevalidate.NameGateway("name", req.Name); err != nil {
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

func applyGatewayMask(g *domain.Gateway, req UpdateGatewayReq) {
	if len(req.UpdateMask) == 0 {
		g.Name = req.Name
		g.Description = req.Description
		g.Labels = req.Labels
		if req.GatewayType != "" {
			g.GatewayType = req.GatewayType
		}
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			g.Name = req.Name
		case "description":
			g.Description = req.Description
		case "labels":
			g.Labels = req.Labels
		case "gateway_type":
			if req.GatewayType != "" {
				g.GatewayType = req.GatewayType
			}
		}
	}
}

// Delete удаляет Gateway.
func (s *GatewayService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete gateway %s", id),
		&vpcv1.DeleteGatewayMetadata{GatewayId: id},
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

// Move переносит Gateway в другой folder.
func (s *GatewayService) Move(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Move gateway %s", id),
		&vpcv1.MoveGatewayMetadata{GatewayId: id},
	)
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
		return anypb.New(domainGatewayToProto(updated))
	})
	return &op, nil
}

// ListOperations возвращает операции для конкретного Gateway.
func (s *GatewayService) ListOperations(ctx context.Context, gwID string, p Pagination) ([]operations.Operation, string, error) {
	if _, err := s.repo.Get(ctx, gwID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: gwID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}

// domainGatewayToProto конвертирует domain.Gateway → vpcv1.Gateway.
//
// Поскольку Gateway имеет oneof gateway (только shared_egress сейчас),
// устанавливаем SharedEgressGateway всегда (default-тип в YC).
func domainGatewayToProto(g *domain.Gateway) *vpcv1.Gateway {
	p := &vpcv1.Gateway{
		Id:          g.ID,
		FolderId:    g.FolderID,
		Name:        g.Name,
		Description: g.Description,
		Labels:      g.Labels,
	}
	// shared_egress — единственный поддерживаемый тип в YC sub-phase.
	p.Gateway = &vpcv1.Gateway_SharedEgressGateway{
		SharedEgressGateway: &vpcv1.SharedEgressGateway{},
	}
	return p
}
