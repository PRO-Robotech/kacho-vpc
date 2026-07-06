// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeleteNetworkInterfaceUseCase — async-delete с precondition «NIC не должен быть
// приаттачен к инстансу» (иначе FailedPrecondition — сначала detach) и cleanup
// address-references.
//
// Worker открывает ОДНУ writer-TX и делает в ней ClearReference всех привязанных
// адресов + Delete NIC + outbox-emit DELETED атомарно (иначе при сбое между ними
// возможен dangling-ref).
type DeleteNetworkInterfaceUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteNetworkInterfaceUseCase создает DeleteNetworkInterfaceUseCase.
func NewDeleteNetworkInterfaceUseCase(r Repo, opsRepo operations.Repo) *DeleteNetworkInterfaceUseCase {
	return &DeleteNetworkInterfaceUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки → Operation → worker.
func (u *DeleteNetworkInterfaceUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}

	op, err := operations.NewFromContext(
		ctx,
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
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// GetForUpdate (row-lock `FOR UPDATE`), а не голый Get: Delete читает
	// address-set NIC, чистит ClearReference ровно этот set и удаляет строку —
	// read-modify-write, который ОБЯЗАН сериализоваться с конкурентным Update
	// (тот тоже берёт GetForUpdate, update.go). На голом Get конкурентный Update
	// мог доаттачить адрес МЕЖДУ snapshot'ом и DML: новый адрес не попадал в
	// snapshot, ClearReference его пропускал, NIC удалялся → адрес навсегда
	// used=true с referrer на удалённый NIC (project-rule #10, TOCTOU-орфан).
	cur, err := w.NetworkInterfaces().GetForUpdate(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if cur.UsedByID != "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"network interface %s is still attached to %s %s; detach it first", id, cur.UsedByType, cur.UsedByID)
	}
	// Снимаем used + referrer со всех привязанных Address-ресурсов в ТОЙ ЖЕ
	// writer-TX, что и Delete NIC (атомарно). Адреса не удаляем, просто
	// освобождаем. ErrNotFound терпим (адрес мог быть уже удален) — остальные
	// ошибки откатывают всю операцию.
	for _, addrID := range append(append([]string{}, cur.V4AddressIDs...), cur.V6AddressIDs...) {
		if cerr := w.Addresses().ClearReference(ctx, addrID); cerr != nil && !errors.Is(cerr, repo.ErrNotFound) {
			return nil, serviceerr.MapRepoErr(cerr)
		}
	}
	if err := w.NetworkInterfaces().Delete(ctx, id); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "NetworkInterface", id, "DELETED", map[string]any{"id": id}); oerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	// Снимаем owner-hierarchy-tuple vpc_network_interface→project в той же
	// writer-TX (projectID — из прочитанной выше строки).
	if rerr := w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(
		fgaregister.ProjectHierarchy(cur.ProjectID, "vpc_network_interface", id),
	)); rerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga unregister intent: %v", repo.ErrInternal, rerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, serviceerr.MapRepoErr(cerr)
	}
	// Ответ операции — google.protobuf.Empty (по proto-контракту Delete).
	return anypb.New(&emptypb.Empty{})
}
