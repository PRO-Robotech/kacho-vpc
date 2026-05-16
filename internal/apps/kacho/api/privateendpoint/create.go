package privateendpoint

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
	pe "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// CreatePrivateEndpointUseCase инициирует создание PrivateEndpoint.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): worker открывает Writer-TX
// и делает Insert(PE) + Outbox.Emit("PrivateEndpoint", …, "CREATED") в одной
// pgx.Tx — атомарность DML + outbox гарантирована. FK на Network/Subnet/Address
// (миграция 0024) проверяются Postgres'ом в момент INSERT, тоже в этой же
// writer-TX.
type CreatePrivateEndpointUseCase struct {
	repo         Repo
	networkRead  NetworkReader
	subnetRead   SubnetReader
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewCreatePrivateEndpointUseCase создаёт CreatePrivateEndpointUseCase.
func NewCreatePrivateEndpointUseCase(r Repo, networkRead NetworkReader, subnetRead SubnetReader, folderClient FolderClient, opsRepo operations.Repo) *CreatePrivateEndpointUseCase {
	return &CreatePrivateEndpointUseCase{
		repo:         r,
		networkRead:  networkRead,
		subnetRead:   subnetRead,
		folderClient: folderClient,
		opsRepo:      opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
//
// Принимает `domain.PrivateEndpoint` напрямую (KAC-94, skill evgeniy §2 B.3 / §7 I.1):
// тривиальная `CreateInput{PrivateEndpoint: …}`-обёртка удалена — она лишь
// перепаковывала domain.X без дополнительного контекста. Поле `p.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixPrivateEndpoint)`.
func (u *CreatePrivateEndpointUseCase) Execute(ctx context.Context, p domain.PrivateEndpoint) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, p.NetworkID); err != nil {
		return nil, err
	}
	// SubnetID — опциональное поле (oneof AddressSpec); валидируем формат только
	// если задано.
	if p.SubnetID != "" {
		if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, p.SubnetID); err != nil {
			return nil, err
		}
	}
	if p.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if p.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}

	// Domain self-validation для name/description/labels.
	if err := p.Validate(); err != nil {
		return nil, err
	}

	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.
	// Sync network/subnet/uniqueness-проверки (через DB-state в той же сервис-БД)
	// остаются — они race-free относительно peer-сервисов.
	if _, err := u.networkRead.Get(ctx, p.NetworkID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", p.NetworkID)
		}
		return nil, mapRepoErr(err)
	}
	if p.SubnetID != "" {
		if _, err := u.subnetRead.Get(ctx, p.SubnetID); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Subnet %s not found", p.SubnetID)
			}
			return nil, mapRepoErr(err)
		}
	}
	name := string(p.Name)
	if name != "" {
		rd, err := u.repo.Reader(ctx)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		existing, _, lerr := rd.PrivateEndpoints().List(ctx, PrivateEndpointFilter{FolderID: p.FolderID, Name: name}, Pagination{})
		_ = rd.Close()
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "PrivateEndpoint with name %s already exists", name)
		}
	}

	peID := ids.NewID(ids.PrefixPrivateEndpoint)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create private endpoint %s", name),
		&pe.CreatePrivateEndpointMetadata{PrivateEndpointId: peID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, peID, p)
	})

	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный backstop:
// folder-exists + Insert (FK ограничения / UNIQUE-нарушения); все DML + outbox
// идут через одну writer-TX (skill evgeniy §6 G.5).
func (u *CreatePrivateEndpointUseCase) doCreate(ctx context.Context, peID string, p domain.PrivateEndpoint) (*anypb.Any, error) {
	exists, err := u.folderClient.Exists(ctx, p.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", p.FolderID)
	}

	if _, err := u.networkRead.Get(ctx, p.NetworkID); err != nil {
		return nil, status.Errorf(codes.NotFound, "Network %s not found", p.NetworkID)
	}
	if p.SubnetID != "" {
		if _, err := u.subnetRead.Get(ctx, p.SubnetID); err != nil {
			return nil, status.Errorf(codes.NotFound, "Subnet %s not found", p.SubnetID)
		}
	}

	stype := p.ServiceType
	if stype == "" {
		stype = domain.PrivateEndpointServiceTypeObjectStorage
	}
	p.ID = peID
	p.ServiceType = stype
	p.Status = domain.PrivateEndpointStatusAvailable

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	created, err := w.PrivateEndpoints().Insert(ctx, &p)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "PrivateEndpoint", created.ID, "CREATED", privateEndpointPayloadMap(created)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalPrivateEndpointRecord(created)
}
