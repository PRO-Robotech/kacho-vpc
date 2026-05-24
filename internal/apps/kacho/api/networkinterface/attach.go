package networkinterface

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// AttachToInstanceUseCase — приаттачить NIC к compute-инстансу.
//
// **Race-safety (KAC-52, workspace CLAUDE.md §«Within-service refs — DB-уровень
// обязателен», запрет #10):** атомарный CAS на repo-уровне (single-statement
// conditional UPDATE с CAS-условием `(used_by_id = ” OR used_by_id = $new)`
// — defined in `internal/repo/kacho/iface_network_interface.go`,
// `internal/repo/kacho/pg/network_interface.go::AttachToInstance`). Software-side
// `Get → check → Update` (TOCTOU) ЗАПРЕЩЁН — этот шаблон привёл к реальному
// инциденту 2026-05-14 (две Compute.Instance.Create указали один NIC, обе
// прошли software-guard, обе вызвали безусловный UPDATE, second writer wins).
//
// Дешёвый Get с понятным error-message для типичного случая (NIC уже attached
// к видимому owner-у) остаётся как software fast-path — но это не race-safety,
// а UX-улучшение: финальная защита от TOCTOU — на DB-уровне.
//
// Wave 5 replicate (KAC-94, NIC batch): worker открывает writer-TX, делает
// AttachToInstance (CAS) + outbox-emit UPDATED атомарно (skill evgeniy §6 G.5).
type AttachToInstanceUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewAttachToInstanceUseCase создаёт AttachToInstanceUseCase.
func NewAttachToInstanceUseCase(r Repo, opsRepo operations.Repo) *AttachToInstanceUseCase {
	return &AttachToInstanceUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки → Operation → worker (с atomic CAS).
//
// index — информационный (на какой слот инстанса вешать NIC); не персистим
// в kacho-vpc (его читает compute-side при wiring).
func (u *AttachToInstanceUseCase) Execute(ctx context.Context, id, instanceID, index string) (*operations.Operation, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	if instanceID == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id required")
	}
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Attach network interface %s to instance %s", id, instanceID),
		&vpcv1.AttachNetworkInterfaceMetadata{NetworkInterfaceId: id, InstanceId: instanceID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	_ = index
	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doAttach(ctx, id, instanceID)
	})
	return &op, nil
}

func (u *AttachToInstanceUseCase) doAttach(ctx context.Context, id, instanceID string) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	// Software fast-path: дешёвый Get с понятным error-message для типичного
	// случая (NIC уже attached к видимому owner-у). НЕ race-safe сам по себе —
	// финальная защита от TOCTOU — на DB-уровне через AttachToInstance(CAS).
	// Однако этот Get внутри той же writer-TX (G.2) — видит uncommitted state
	// этого же writer'а, что гарантирует консистентность сообщения.
	cur, err := w.NetworkInterfaces().Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if cur.UsedByID != "" && cur.UsedByID != instanceID {
		return nil, status.Errorf(codes.FailedPrecondition,
			"network interface %s is already attached to %s %s", id, cur.UsedByType, cur.UsedByID)
	}
	updated, err := w.NetworkInterfaces().AttachToInstance(ctx, id, niUsedByReferrerType, instanceID, "")
	if err != nil {
		// CAS-конфликт: repo вернул ErrFailedPrecondition (single-statement
		// UPDATE на одной row защищён row-level lock-ом Postgres; параллельный
		// writer ждёт commit-а первого, видит уже обновлённый row, CAS не
		// matches → 0 rows из RETURNING → ErrFailedPrecondition). Race-обогащённый
		// message — догружаем actual owner для пользователя (через Get в том
		// же writer'е до Abort).
		if errors.Is(err, repo.ErrFailedPrecondition) {
			if actual, gerr := w.NetworkInterfaces().Get(ctx, id); gerr == nil && actual.UsedByID != "" {
				return nil, status.Errorf(codes.FailedPrecondition,
					"network interface %s is already attached to %s %s", id, actual.UsedByType, actual.UsedByID)
			}
			return nil, status.Errorf(codes.FailedPrecondition,
				"network interface %s attach raced; already owned by another resource", id)
		}
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
