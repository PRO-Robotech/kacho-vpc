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

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// AttachToInstanceUseCase — приаттачить NIC к compute-инстансу.
//
// **Race-safety (KAC-52, workspace CLAUDE.md §«Within-service refs — DB-уровень
// обязателен», запрет #10):** атомарный CAS на repo-уровне (миграция 0016 —
// ON CONFLICT DO UPDATE WHERE used_by_id=” OR used_by_id=$new RETURNING …).
// Software-side `Get → check → Update` (TOCTOU) ЗАПРЕЩЁН — этот шаблон привёл
// к реальному инциденту 2026-05-14 (две Compute.Instance.Create указали один
// NIC, обе прошли software-guard, обе вызвали безусловный UPDATE, second writer
// wins). Use-case ниже использует repo.SetUsedBy, который имплементирован как
// single-statement conditional UPDATE.
//
// Дешёвый Get с понятным error-message для типичного случая (NIC уже attached
// к видимому owner-у) остаётся как software fast-path — но это не race-safety,
// а UX-улучшение: финальная защита от TOCTOU делается на DB-уровне.
type AttachToInstanceUseCase struct {
	repo    NetworkInterfaceRepo
	opsRepo operations.Repo
}

// NewAttachToInstanceUseCase создаёт AttachToInstanceUseCase.
func NewAttachToInstanceUseCase(repo NetworkInterfaceRepo, opsRepo operations.Repo) *AttachToInstanceUseCase {
	return &AttachToInstanceUseCase{repo: repo, opsRepo: opsRepo}
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
	op, err := operations.New(
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
		// Software fast-path: дешёвый Get с понятным error-message для типичного
		// случая (NIC уже attached к видимому owner-у). НЕ race-safe сам по себе —
		// финальная защита от TOCTOU делается на DB-уровне в repo.SetUsedBy.
		cur, err := u.repo.Get(ctx, id)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		if cur.UsedByID != "" && cur.UsedByID != instanceID {
			return nil, status.Errorf(codes.FailedPrecondition,
				"network interface %s is already attached to %s %s", id, cur.UsedByType, cur.UsedByID)
		}
		updated, err := u.repo.SetUsedBy(ctx, id, niUsedByReferrerType, instanceID, "", domain.NIStatusActive)
		if err != nil {
			// CAS-конфликт: repo вернул ErrFailedPrecondition (single-statement
			// UPDATE на одной row защищён row-level lock-ом Postgres; параллельный
			// writer ждёт commit-а первого, видит уже обновлённый row, CAS не
			// matches → 0 rows из RETURNING → ErrFailedPrecondition). Race-обогащённый
			// message — догружаем actual owner для пользователя.
			if errors.Is(err, ports.ErrFailedPrecondition) {
				if actual, gerr := u.repo.Get(ctx, id); gerr == nil && actual.UsedByID != "" {
					return nil, status.Errorf(codes.FailedPrecondition,
						"network interface %s is already attached to %s %s", id, actual.UsedByType, actual.UsedByID)
				}
				return nil, status.Errorf(codes.FailedPrecondition,
					"network interface %s attach raced; already owned by another resource", id)
			}
			return nil, mapRepoErr(err)
		}
		return marshalNetworkInterfaceRecord(updated)
	})
	return &op, nil
}
