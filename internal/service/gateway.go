package service

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// type2pb регистрирует DTO-трансферы в init() — нужны для dto.Transfer.
	// Skill evgeniy §3 C.4.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
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
func (s *GatewayService) Get(ctx context.Context, id string) (*domain.GatewayRecord, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return g, nil
}

// List возвращает список Gateways.
// folder_id обязателен (R10 #C1 closure).
func (s *GatewayService) List(ctx context.Context, f GatewayFilter, p Pagination) ([]*domain.GatewayRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание Gateway, возвращает Operation.
//
// Wave 2 batch B (KAC-94): description/labels — через domain.Gateway.Validate().
// Name по-прежнему проходит через corevalidate.NameGateway (strict-name regex,
// verbatim YC), т.к. RcNameVPC — permissive (Wave 3 заменит на RcNameGateway).
func (s *GatewayService) Create(ctx context.Context, req CreateGatewayReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	// Gateway.Name — strict regex (lowercase, без uppercase/underscore — verbatim YC).
	// Wave 2 batch B держит это в service-слое до появления RcNameGateway newtype.
	if err := corevalidate.NameGateway("name", req.Name); err != nil {
		return nil, err
	}
	// Domain self-validation для description/labels (skill evgeniy §4 D.5 / AP-1).
	g := domain.Gateway{
		FolderID:    req.FolderID,
		Name:        domain.RcNameVPC(req.Name), // permissive newtype — strict выше через NameGateway
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
		GatewayType: domain.GatewayType(req.GatewayType),
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	// Verbatim YC (probe 2026-05-11): gateway-type oneof обязателен — без него (или с
	// нераспознанным телом, напр. `sharedEgressGateway` вместо `sharedEgressGatewaySpec`)
	// YC отвечает InvalidArgument "Illegal argument gateway". Сейчас единственный тип —
	// shared_egress (SharedEgressGatewaySpec). kacho-vpc#9.
	if g.GatewayType != domain.GatewayTypeSharedEgress {
		return nil, status.Error(codes.InvalidArgument, "Illegal argument gateway")
	}

	// Verbatim YC: folder existence — sync precondition до Operation (async-проверка
	// в doCreate остаётся backstop'ом). NB: имена Gateway в YC НЕ уникальны (probe
	// 2026-05-11) — name-uniqueness тут НЕ проверяем (в отличие от Network/Subnet/RT/SG). kacho-vpc#8/#9.
	if err := checkFolderExists(ctx, s.folderClient, req.FolderID); err != nil {
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

	gtype := domain.GatewayType(req.GatewayType)
	if gtype == "" {
		gtype = domain.GatewayTypeSharedEgress
	}

	g := &domain.Gateway{
		ID:          gwID,
		FolderID:    req.FolderID,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
		GatewayType: gtype,
	}
	created, err := s.repo.Insert(ctx, g)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalGatewayRecord(created)
}

// marshalGatewayRecord конвертирует repo-entity Gateway в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4: protoconv.Gateway → dto.Transfer).
func marshalGatewayRecord(rec *domain.GatewayRecord) (*anypb.Any, error) {
	var dst *vpcv1.Gateway
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Gateway: %w", err)
	}
	return anypb.New(dst)
}

// Update обновляет Gateway.
func (s *GatewayService) Update(ctx context.Context, req UpdateGatewayReq) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, req.GatewayID); err != nil {
		return nil, err
	}
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
	rec, err := s.repo.Get(ctx, req.GatewayID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applyGatewayMask(&rec.Gateway, req)
	updated, err := s.repo.Update(ctx, &rec.Gateway)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalGatewayRecord(updated)
}

// validateGatewayUpdate — sync-проверка update_mask и значений.
//
// Wave 2 batch B (KAC-94): description/labels — через domain newtype.Validate().
// Name по-прежнему через corevalidate.NameGateway (strict-name).
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
			if err := domain.RcDescription(req.Description).Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(domain.LabelsFromMap(req.Labels)); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyGatewayMask(g *domain.Gateway, req UpdateGatewayReq) {
	if len(req.UpdateMask) == 0 {
		g.Name = domain.RcNameVPC(req.Name)
		g.Description = domain.RcDescription(req.Description)
		g.Labels = domain.LabelsFromMap(req.Labels)
		if req.GatewayType != "" {
			g.GatewayType = domain.GatewayType(req.GatewayType)
		}
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			g.Name = domain.RcNameVPC(req.Name)
		case "description":
			g.Description = domain.RcDescription(req.Description)
		case "labels":
			g.Labels = domain.LabelsFromMap(req.Labels)
		case "gateway_type":
			if req.GatewayType != "" {
				g.GatewayType = domain.GatewayType(req.GatewayType)
			}
		}
	}
}

// Delete удаляет Gateway.
func (s *GatewayService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
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
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
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
		return marshalGatewayRecord(updated)
	})
	return &op, nil
}

// ListOperations возвращает операции для конкретного Gateway.
func (s *GatewayService) ListOperations(ctx context.Context, gwID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, gwID); err != nil {
		return nil, "", err
	}
	if _, err := s.repo.Get(ctx, gwID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: gwID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}

//
// Поскольку Gateway имеет oneof gateway (только shared_egress сейчас),
// устанавливаем SharedEgressGateway всегда (default-тип в YC).
