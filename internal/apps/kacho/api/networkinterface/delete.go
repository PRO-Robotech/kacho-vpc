package networkinterface

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeleteNetworkInterfaceUseCase — async-delete с precondition «NIC не должен быть
// приаттачен к инстансу» (FailedPrecondition; detach сначала) и cleanup
// address-references.
//
// Wave 5 replicate (KAC-94, NIC batch): worker открывает writer-TX, делает Delete
// + outbox-emit DELETED tombstone атомарно (skill evgeniy §6 G.5). Address-cleanup
// — пока legacy AddressRepo (отдельная TX).
type DeleteNetworkInterfaceUseCase struct {
	repo        Repo
	addressRepo AddressRepo
	opsRepo     operations.Repo
}

// NewDeleteNetworkInterfaceUseCase создаёт DeleteNetworkInterfaceUseCase.
func NewDeleteNetworkInterfaceUseCase(r Repo, addressRepo AddressRepo, opsRepo operations.Repo) *DeleteNetworkInterfaceUseCase {
	return &DeleteNetworkInterfaceUseCase{repo: r, addressRepo: addressRepo, opsRepo: opsRepo}
}

// Execute — sync-проверки → Operation → worker.
func (u *DeleteNetworkInterfaceUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete network interface %s", id),
		&vpcv1.DeleteNetworkInterfaceMetadata{NetworkInterfaceId: id},
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

func (u *DeleteNetworkInterfaceUseCase) doDelete(ctx context.Context, id string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	cur, err := w.NetworkInterfaces().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if cur.UsedByID != "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"network interface %s is still attached to %s %s; detach it first", id, cur.UsedByType, cur.UsedByID)
	}
	// Снимаем used + referrer со всех привязанных Address-ресурсов (адреса
	// не удаляем — они остаются доступными, просто свободными). Address-cleanup —
	// отдельная TX (legacy `AddressRepo.ClearReference`); не атомарно с Delete
	// NIC. Если crash между Delete NIC и ClearReference — address останется
	// помеченным как used, ссылаясь на удалённый NIC; grace-recovery: tenant
	// удалит Address (его referrer-row уже dangling), и `addresses.used`
	// сбросится. См. workspace CLAUDE.md §«Кросс-доменные ссылки на ресурсы»
	// (тот же грациозный dangling-ref паттерн).
	for _, addrID := range append(append([]string{}, cur.V4AddressIDs...), cur.V6AddressIDs...) {
		_ = u.addressRepo.ClearReference(ctx, addrID)
	}
	if err := w.NetworkInterfaces().Delete(ctx, id); err != nil {
		return nil, mapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "NetworkInterface", id, "DELETED", map[string]any{"id": id}); oerr != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, mapRepoErr(cerr)
	}
	// proto-options: response = google.protobuf.Empty (verbatim YC).
	return anypb.New(&emptypb.Empty{})
}
