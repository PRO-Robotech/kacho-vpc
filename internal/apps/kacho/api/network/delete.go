package network

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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeleteNetworkUseCase — sync FAILED_PRECONDITION если в Network есть subnets /
// route tables / non-default SG. Async-часть (worker): удалить default SG (если
// есть), потом сам Network. FK RESTRICT — атомарный backstop.
//
// Wave 5 pilot (KAC-94): Network.Delete + outbox-emit DELETED в одной CQRS-TX.
// Default-SG cleanup — legacy (SG-repo не CQRS pilot).
type DeleteNetworkUseCase struct {
	repo           Repo
	subnetReader   SubnetReader      // may be nil → skip child class
	routeTableRead RouteTableReader  // may be nil
	sgRepo         SecurityGroupRepo // may be nil → skip default-SG cleanup
	opsRepo        operations.Repo
}

// NewDeleteNetworkUseCase создаёт DeleteNetworkUseCase. Все child-reader'ы
// необязательны: nil → пропускаем соответствующий child-класс (для unit-тестов
// со scoped wiring).
func NewDeleteNetworkUseCase(r Repo, subnetReader SubnetReader, routeTableRead RouteTableReader, sgRepo SecurityGroupRepo, opsRepo operations.Repo) *DeleteNetworkUseCase {
	return &DeleteNetworkUseCase{
		repo:           r,
		subnetReader:   subnetReader,
		routeTableRead: routeTableRead,
		sgRepo:         sgRepo,
		opsRepo:        opsRepo,
	}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeleteNetworkUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := u.checkNetworkEmpty(ctx, id); err != nil {
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
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doDelete(ctx, id)
	})

	return &op, nil
}

// doDelete — async-часть Delete. Wave 5 (KAC-94): Default-SG cleanup идёт через
// legacy sgRepo (отдельный TX внутри него), Network.Delete + outbox-emit
// DELETED — через CQRS writer-TX.
func (u *DeleteNetworkUseCase) doDelete(ctx context.Context, id string) (*anypb.Any, error) {
	// Default-SG cleanup. Перед удалением Network — удалить связанный default SG
	// (FK RESTRICT). Не-default SG — preserve, FK не даст удалить Network ⇒
	// FAILED_PRECONDITION. Cleanup идёт через legacy sgRepo (KAC-94 SG-repo
	// пока не CQRS).
	if u.sgRepo != nil {
		rd, err := u.repo.Reader(ctx)
		if err == nil {
			n, gerr := rd.Networks().Get(ctx, id)
			_ = rd.Close()
			if gerr == nil && n.DefaultSecurityGroupID != "" {
				_ = u.sgRepo.Delete(ctx, n.DefaultSecurityGroupID)
			}
		}
	}

	// Сам Delete + outbox-DELETED атомарны в одной CQRS-TX.
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	if err := w.Networks().Delete(ctx, id); err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", id, "DELETED", map[string]any{"id": id}); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(&emptypb.Empty{})
}

// checkNetworkEmpty — sync FAILED_PRECONDITION if the network still has
// subnets / route tables / non-default security groups (verbatim YC text:
// "Network <id> is not empty"). Reader'ы могут быть nil — тогда соответствующий
// child-класс не проверяется. См. kacho-vpc#8.
func (u *DeleteNetworkUseCase) checkNetworkEmpty(ctx context.Context, networkID string) error {
	notEmpty := func() error {
		return status.Errorf(codes.FailedPrecondition, "Network %s is not empty", networkID)
	}
	if u.subnetReader != nil {
		subs, _, err := u.subnetReader.List(ctx, SubnetFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return mapRepoErr(err)
		}
		if len(subs) > 0 {
			return notEmpty()
		}
	}
	if u.routeTableRead != nil {
		rts, _, err := u.routeTableRead.List(ctx, RouteTableFilter{NetworkID: networkID}, Pagination{})
		if err != nil {
			return mapRepoErr(err)
		}
		if len(rts) > 0 {
			return notEmpty()
		}
	}
	if u.sgRepo != nil {
		sgs, _, err := u.sgRepo.List(ctx, SecurityGroupFilter{NetworkID: networkID}, Pagination{})
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
