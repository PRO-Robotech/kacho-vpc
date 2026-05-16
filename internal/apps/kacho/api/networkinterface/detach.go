package networkinterface

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DetachFromInstanceUseCase — отвязать NIC от инстанса.
//
// `writer.NetworkInterfaces().DetachFromInstance(id)` — idempotent UPDATE:
// затирает used_by_* колонки и переводит status в AVAILABLE. Идемпотентно
// (повторный Detach на уже свободном NIC даёт тот же результат).
//
// Wave 5 replicate (KAC-94, NIC batch): worker открывает writer-TX, делает
// DetachFromInstance + outbox-emit UPDATED атомарно (skill evgeniy §6 G.5).
type DetachFromInstanceUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDetachFromInstanceUseCase создаёт DetachFromInstanceUseCase.
func NewDetachFromInstanceUseCase(r Repo, opsRepo operations.Repo) *DetachFromInstanceUseCase {
	return &DetachFromInstanceUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки → Operation → worker.
func (u *DetachFromInstanceUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Detach network interface %s", id),
		&vpcv1.DetachNetworkInterfaceMetadata{NetworkInterfaceId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doDetach(ctx, id)
	})
	return &op, nil
}

func (u *DetachFromInstanceUseCase) doDetach(ctx context.Context, id string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	updated, err := w.NetworkInterfaces().DetachFromInstance(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "NetworkInterface", updated.ID, "UPDATED", networkInterfacePayloadMap(updated)); oerr != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, mapRepoErr(cerr)
	}
	return marshalNetworkInterfaceRecord(updated)
}
