package networkinterface

import (
	"fmt"

	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// DeleteNetworkInterfaceUseCase — async-delete с precondition «NIC не должен быть
// приаттачен к инстансу» (FailedPrecondition; detach сначала) и cleanup
// address-references.
type DeleteNetworkInterfaceUseCase struct {
	repo        NetworkInterfaceRepo
	addressRepo AddressRepo
	opsRepo     operations.Repo
}

// NewDeleteNetworkInterfaceUseCase создаёт DeleteNetworkInterfaceUseCase.
func NewDeleteNetworkInterfaceUseCase(repo NetworkInterfaceRepo, addressRepo AddressRepo, opsRepo operations.Repo) *DeleteNetworkInterfaceUseCase {
	return &DeleteNetworkInterfaceUseCase{repo: repo, addressRepo: addressRepo, opsRepo: opsRepo}
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
		cur, err := u.repo.Get(ctx, id)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		if cur.UsedByID != "" {
			return nil, status.Errorf(codes.FailedPrecondition,
				"network interface %s is still attached to %s %s; detach it first", id, cur.UsedByType, cur.UsedByID)
		}
		// Снимаем used + referrer со всех привязанных Address-ресурсов (адреса
		// не удаляем — они остаются доступными, просто свободными).
		for _, addrID := range append(append([]string{}, cur.V4AddressIDs...), cur.V6AddressIDs...) {
			_ = u.addressRepo.ClearReference(ctx, addrID)
		}
		if err := u.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		// proto-options: response = google.protobuf.Empty (verbatim YC).
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}
