package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	repo           NetworkRepo
	subnetRepo     SubnetRepo
	routeTableRepo RouteTableRepo
	sgService      *SecurityGroupService // для создания default SG (может быть nil в тестах)
	folderClient   FolderClient
	opsRepo        operations.Repo
}

// NewNetworkService создаёт NetworkService.
//
// subnetRepo / routeTableRepo используются для per-network children endpoints
// (ListSubnets / ListRouteTables). Могут быть nil — тогда соответствующие
// методы вернут empty list.
//
// sgService nil — disable auto-create default SG (для unit-тестов или
// если фича отключена).
func NewNetworkService(repo NetworkRepo, subnetRepo SubnetRepo, routeTableRepo RouteTableRepo, sgService *SecurityGroupService, folderClient FolderClient, opsRepo operations.Repo) *NetworkService {
	return &NetworkService{
		repo:           repo,
		subnetRepo:     subnetRepo,
		routeTableRepo: routeTableRepo,
		sgService:      sgService,
		folderClient:   folderClient,
		opsRepo:        opsRepo,
	}
}

// ListSubnets возвращает подсети, принадлежащие данной сети.
// Перед вызовом проверяется наличие самой сети (NotFound — verbatim YC).
func (s *NetworkService) ListSubnets(ctx context.Context, networkID string, p Pagination) ([]*domain.Subnet, string, error) {
	if _, err := s.repo.Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if s.subnetRepo == nil {
		return nil, "", nil
	}
	return s.subnetRepo.List(ctx, SubnetFilter{NetworkID: networkID}, p)
}

// ListSecurityGroups возвращает SG, привязанные к данной сети.
func (s *NetworkService) ListSecurityGroups(ctx context.Context, networkID string, p Pagination) ([]*domain.SecurityGroup, string, error) {
	if _, err := s.repo.Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if s.sgService == nil {
		return nil, "", nil
	}
	return s.sgService.repo.List(ctx, SecurityGroupFilter{NetworkID: networkID}, p)
}

// ListRouteTables возвращает route tables, привязанные к данной сети.
func (s *NetworkService) ListRouteTables(ctx context.Context, networkID string, p Pagination) ([]*domain.RouteTable, string, error) {
	if _, err := s.repo.Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if s.routeTableRepo == nil {
		return nil, "", nil
	}
	return s.routeTableRepo.List(ctx, RouteTableFilter{NetworkID: networkID}, p)
}

// ListOperations возвращает операции, относящиеся к данному ресурсу (по resource_id).
func (s *NetworkService) ListOperations(ctx context.Context, networkID string, p Pagination) ([]operations.Operation, string, error) {
	if _, err := s.repo.Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: networkID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
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
	// VPC Network принимает empty name (verbatim YC: regex с empty-allowed).
	// См. YC-DIFF-NAME-VALIDATION.md — YC permissive policy для VPC.
	// Note: garbage-UUID folder_id больше НЕ валидируется sync — async через
	// folderClient.Exists → NotFound (verbatim YC). См. YC-DIFF-INVALID-PARENT-CODE.md.
	if err := corevalidate.NameVPC("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}

	netID := ids.NewID(ids.PrefixNetwork)
	op, err := operations.New(
		ids.PrefixOperationVPC,
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
		// verbatim YC text: "Folder with id <X> not found".
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
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

	// Pure: НЕ создаём default SG здесь. Это делает kacho-vpc-controllers
	// (default-sg reconciler-loop) асинхронно. См.
	// POST-PROCESSING-IN-CONTROLLERS.md.
	return anypb.New(domainNetworkToProto(created))
}

// Update обновляет Network.
//
// Sync-валидация update_mask и значений выполняется ДО Operation: каждое поле,
// упомянутое в mask, проверяется по тем же правилам, что и Create. Без mask —
// валидируются все три поля (name/description/labels). См. validateNetworkUpdate.
func (s *NetworkService) Update(ctx context.Context, req UpdateNetworkReq) (*operations.Operation, error) {
	if req.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := validateNetworkUpdate(req); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
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

// validateNetworkUpdate — sync-проверка update_mask и значений.
//
// Decision table (для каждого поля в mask):
//   - name      → должно быть non-empty + соответствовать verbatim YC name regex.
//   - description → длина <=256 chars (utf-8 runes).
//   - labels    → <=64 пар, ключ/значение — verbatim YC.
//
// Поле, не упомянутое в mask, не валидируется (= unchanged).
func validateNetworkUpdate(req UpdateNetworkReq) error {
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}}
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
			// VPC Network: empty name allowed (YC permissive policy).
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

// Move инициирует перенос Network в другой folder, возвращает Operation.
//
// Sync-валидация: destinationFolderId required.
// Async (внутри Operation worker): destination folder Exists через folderClient.
// Если folder не найден → Operation.error: NotFound "Folder with id X not found" (verbatim YC).
func (s *NetworkService) Move(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Move network %s", id),
		&vpcv1.MoveNetworkMetadata{NetworkId: id},
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
		return anypb.New(domainNetworkToProto(updated))
	})

	return &op, nil
}

// Delete удаляет Network.
func (s *NetworkService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
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
		// Перед удалением Network — удалить связанный default SG (FK RESTRICT).
		// Не-default SG — preserve, FK не даст удалить Network ⇒ FAILED_PRECONDITION.
		if s.sgService != nil {
			n, gerr := s.repo.Get(ctx, id)
			if gerr == nil && n.DefaultSecurityGroupID != "" {
				_ = s.sgService.repo.Delete(ctx, n.DefaultSecurityGroupID)
			}
		}
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
// errors.Is используется потому, что repo может оборачивать sentinel через %w
// (например, ErrFailedPrecondition + контекст "network is not empty").
//
// Sentinel-prefix (`failed precondition: `, `not found`, ...) удаляется при
// преобразовании в gRPC-сообщение, чтобы клиент видел verbatim YC text без
// internal-обёртки. См. YC-DIFF-CIDR-ERROR-SHAPE.md.
func mapRepoErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return status.Error(codes.NotFound, stripSentinel(err, ErrNotFound))
	}
	if errors.Is(err, ErrAlreadyExists) {
		return status.Error(codes.AlreadyExists, stripSentinel(err, ErrAlreadyExists))
	}
	if errors.Is(err, ErrFailedPrecondition) {
		return status.Error(codes.FailedPrecondition, stripSentinel(err, ErrFailedPrecondition))
	}
	if errors.Is(err, ErrInvalidArg) {
		return status.Error(codes.InvalidArgument, stripSentinel(err, ErrInvalidArg))
	}
	if errors.Is(err, ErrInternal) {
		// Generic Internal без leak'а pgx-текста.
		return status.Error(codes.Internal, "internal database error")
	}
	return err
}

// stripSentinel — извлекает «полезную» часть сообщения (после «sentinel: »),
// чтобы выдать клиенту verbatim text без internal-обёртки sentinel-ошибки.
// Если err == sentinel или контекст не добавлен, возвращает sentinel.Error().
func stripSentinel(err, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		return rest
	}
	return msg
}
