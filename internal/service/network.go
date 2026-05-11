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
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
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
	sgRepo         SecurityGroupRepo     // для inline default-SG creation в worker'е
	folderClient   FolderClient
	opsRepo        operations.Repo
}

// NewNetworkService создаёт NetworkService.
//
// subnetRepo / routeTableRepo используются для per-network children endpoints
// (ListSubnets / ListRouteTables). Могут быть nil — тогда соответствующие
// методы вернут empty list. sgService nil — disable auto-create/cleanup default SG.
//
// sgRepo: если не nil — Network.Create синхронно создаёт inline default SG в
// worker'е (KACHO_VPC_DEFAULT_SG_INLINE=true). Если nil — Network.Create не создаёт SG
// (verbatim YC: SG создаётся внешним reconciler'ом). Передаётся конструктором, а не
// setter'ом — composition root решает по флагу до сборки сервиса.
func NewNetworkService(repo NetworkRepo, subnetRepo SubnetRepo, routeTableRepo RouteTableRepo, sgService *SecurityGroupService, folderClient FolderClient, opsRepo operations.Repo, sgRepo SecurityGroupRepo) *NetworkService {
	return &NetworkService{
		repo:           repo,
		subnetRepo:     subnetRepo,
		routeTableRepo: routeTableRepo,
		sgService:      sgService,
		sgRepo:         sgRepo,
		folderClient:   folderClient,
		opsRepo:        opsRepo,
	}
}

// ListSubnets возвращает подсети, принадлежащие данной сети.
// Перед вызовом проверяется наличие самой сети (NotFound — verbatim YC).
func (s *NetworkService) ListSubnets(ctx context.Context, networkID string, p Pagination) ([]*domain.Subnet, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
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
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
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
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
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
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
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
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, id); err != nil {
		return nil, err
	}
	n, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return n, nil
}

// List возвращает список сетей в folder с пагинацией.
//
// folder_id обязателен (verbatim YC: oneof container с exactly_one). Без
// service-level enforcement repo тихо пропускал бы WHERE folder_id и
// возвращал кросс-folder enumeration — closed R10 critical (#C1).
func (s *NetworkService) List(ctx context.Context, f NetworkFilter, p Pagination) ([]*domain.Network, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
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

	// Verbatim YC: existence / uniqueness checks run synchronously, BEFORE the
	// Operation is created (clients get a sync gRPC error, not a 200+Operation
	// that fails async). The async copies in doCreate stay as the atomic
	// backstop. См. kacho-vpc#8.
	if err := checkFolderExists(ctx, s.folderClient, req.FolderID); err != nil {
		return nil, err
	}
	if req.Name != "" {
		existing, _, lerr := s.repo.List(ctx, NetworkFilter{FolderID: req.FolderID, Name: req.Name}, Pagination{})
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "Network with name %s already exists", req.Name)
		}
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
		// Маппим service.ErrAlreadyExists (UNIQUE folder_id+name, миграция 0018)
		// и прочие в gRPC status — иначе worker положит raw error с code=Unknown.
		return nil, mapRepoErr(err)
	}

	// Inline default SG. Verbatim YC defaults: 2 правила INGRESS+EGRESS, protocol ANY, cidr 0.0.0.0/0.
	if s.sgRepo != nil {
		shortNet := created.ID
		if len(shortNet) > 8 {
			shortNet = shortNet[:8]
		}
		sg := &domain.SecurityGroup{
			ID:                ids.NewID(ids.PrefixSecurityGroup),
			FolderID:          created.FolderID,
			NetworkID:         created.ID,
			CreatedAt:         time.Now().UTC(),
			Name:              "default-sg-" + shortNet,
			Description:       "Default security group (auto-created by kacho-vpc)",
			Status:            "ACTIVE",
			DefaultForNetwork: true,
			Rules: []domain.SecurityGroupRule{
				{Direction: "INGRESS", ProtocolName: "ANY", ProtocolNumber: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
				{Direction: "EGRESS", ProtocolName: "ANY", ProtocolNumber: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
			},
		}
		createdSG, sgErr := s.sgRepo.Insert(ctx, sg)
		if sgErr != nil {
			// SG creation failed — Network уже создан. Log warn, не падаем
			// (admin может создать default SG руками через public API).
			// Возвращаем network без default_security_group_id.
			return anypb.New(protoconv.Network(created))
		}
		// Bind SG как default через NetworkRepo.Update.
		created.DefaultSecurityGroupID = createdSG.ID
		updated, uerr := s.repo.Update(ctx, created)
		if uerr == nil {
			return anypb.New(protoconv.Network(updated))
		}
		// Update failed — возвращаем без bind'а (orphan SG, admin зачистит).
		return anypb.New(protoconv.Network(created))
	}
	return anypb.New(protoconv.Network(created))
}

// Update обновляет Network.
//
// Sync-валидация update_mask и значений выполняется ДО Operation: каждое поле,
// упомянутое в mask, проверяется по тем же правилам, что и Create. Без mask —
// валидируются все три поля (name/description/labels). См. validateNetworkUpdate.
func (s *NetworkService) Update(ctx context.Context, req UpdateNetworkReq) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, req.NetworkID); err != nil {
		return nil, err
	}
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
		// mapRepoErr: sentinel→gRPC. Без обёртки worker отдавал бы
		// raw "already exists" в Operation.error (verbatim YC parity break).
		return nil, mapRepoErr(err)
	}
	return anypb.New(protoconv.Network(updated))
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
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	// Verbatim YC: destination folder existence is a sync precondition.
	if err := checkFolderExists(ctx, s.folderClient, destFolderID); err != nil {
		return nil, err
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
		return anypb.New(protoconv.Network(updated))
	})

	return &op, nil
}

// Delete удаляет Network.
func (s *NetworkService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// Verbatim YC: a Network with children (subnets / route tables / non-default
	// security groups) can not be deleted — sync FAILED_PRECONDITION. The async
	// FK RESTRICT path in the worker stays as the atomic backstop. См. kacho-vpc#8.
	if err := s.checkNetworkEmpty(ctx, id); err != nil {
		return nil, err
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
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}

// checkNetworkEmpty — sync FAILED_PRECONDITION if the network still has
// subnets / route tables / non-default security groups (verbatim YC text:
// "Network <id> is not empty"). repo-handles may be nil in test wiring → skip
// that child class. См. kacho-vpc#8.
func (s *NetworkService) checkNetworkEmpty(ctx context.Context, networkID string) error {
	notEmpty := func() error {
		return status.Errorf(codes.FailedPrecondition, "Network %s is not empty", networkID)
	}
	if s.subnetRepo != nil {
		subs, _, err := s.subnetRepo.List(ctx, SubnetFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return mapRepoErr(err)
		}
		if len(subs) > 0 {
			return notEmpty()
		}
	}
	if s.routeTableRepo != nil {
		rts, _, err := s.routeTableRepo.List(ctx, RouteTableFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return mapRepoErr(err)
		}
		if len(rts) > 0 {
			return notEmpty()
		}
	}
	if s.sgRepo != nil {
		sgs, _, err := s.sgRepo.List(ctx, SecurityGroupFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return mapRepoErr(err)
		}
		for _, sg := range sgs {
			if !sg.DefaultForNetwork {
				return notEmpty()
			}
		}
	}
	return nil
}

// Используется в worker-goroutines для формирования Operation.Response.

// mapRepoErr переводит domain-ошибки репозитория в gRPC-статусы.
// errors.Is используется потому, что repo может оборачивать sentinel через %w
// (например, ErrFailedPrecondition + контекст "network is not empty").
//
// mapRepoErr / stripSentinel переехали в maperr.go (ранее был только тут,
// что давало неявную зависимость network.go ↔ все service-файлы).
