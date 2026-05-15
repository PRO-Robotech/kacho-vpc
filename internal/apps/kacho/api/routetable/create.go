package routetable

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// CreateInput — параметры для CreateRouteTableUseCase.Execute.
type CreateInput struct {
	RouteTable domain.RouteTable // несёт FolderID/NetworkID/Name/Description/Labels/StaticRoutes
}

// CreateRouteTableUseCase инициирует создание RouteTable. Sync-проверки (folder
// exists, parent network exists, name unique, static_routes валидны)
// выполняются ДО создания Operation.
type CreateRouteTableUseCase struct {
	repo         RouteTableRepo
	networkRead  NetworkReader
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewCreateRouteTableUseCase создаёт CreateRouteTableUseCase.
func NewCreateRouteTableUseCase(repo RouteTableRepo, networkRead NetworkReader, folderClient FolderClient, opsRepo operations.Repo) *CreateRouteTableUseCase {
	return &CreateRouteTableUseCase{
		repo:         repo,
		networkRead:  networkRead,
		folderClient: folderClient,
		opsRepo:      opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
func (u *CreateRouteTableUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	rt := in.RouteTable
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, rt.NetworkID); err != nil {
		return nil, err
	}
	if rt.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if rt.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// Domain self-validation (skill evgeniy §4 D.5 / AP-1).
	if err := rt.Validate(); err != nil {
		return nil, err
	}
	// RT-CIDR-VALIDATION.
	if err := validateStaticRoutes(rt.StaticRoutes); err != nil {
		return nil, err
	}

	// Verbatim YC: existence / uniqueness checks run synchronously, BEFORE the
	// Operation. The async copies in doCreate stay as defensive backstops.
	if err := checkFolderExists(ctx, u.folderClient, rt.FolderID); err != nil {
		return nil, err
	}
	if _, err := u.networkRead.Get(ctx, rt.NetworkID); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", rt.NetworkID)
		}
		return nil, mapRepoErr(err)
	}
	name := string(rt.Name)
	if name != "" {
		existing, _, lerr := u.repo.List(ctx, RouteTableFilter{FolderID: rt.FolderID, Name: name}, Pagination{})
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "RouteTable with name %s already exists", name)
		}
	}

	rtID := ids.NewID(ids.PrefixRouteTable)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create route table %s", name),
		&vpcv1.CreateRouteTableMetadata{RouteTableId: rtID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, rtID, rt)
	})

	return &op, nil
}

// doCreate — async-часть Create.
func (u *CreateRouteTableUseCase) doCreate(ctx context.Context, rtID string, rt domain.RouteTable) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, rt.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", rt.FolderID)
	}
	if _, err := u.networkRead.Get(ctx, rt.NetworkID); err != nil {
		return nil, status.Errorf(codes.NotFound, "Network %s not found", rt.NetworkID)
	}
	rt.ID = rtID
	created, err := u.repo.Insert(ctx, &rt)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalRouteTableRecord(created)
}
