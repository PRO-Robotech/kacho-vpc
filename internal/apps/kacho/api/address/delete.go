// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// DeleteAddressUseCase — sync FAILED_PRECONDITION при deletion_protection=true
// или при «адрес в использовании» (referrer-row). Async-часть (worker):
// освобождение external_ipv6 → DELETE address → возврат v4 IP в freelist +
// outbox-emit Address.DELETED — все в одной writer-TX.
type DeleteAddressUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewDeleteAddressUseCase создает DeleteAddressUseCase.
func NewDeleteAddressUseCase(r Repo, opsRepo operations.Repo) *DeleteAddressUseCase {
	return &DeleteAddressUseCase{repo: r, opsRepo: opsRepo}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeleteAddressUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}

	// Sync pre-check через Reader-TX (deletion_protection + Used).
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	existing, err := rd.Addresses().Get(ctx, id)
	if err != nil {
		_ = rd.Close()
		return nil, serviceerr.MapRepoErr(err)
	}
	if existing.DeletionProtection {
		_ = rd.Close()
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has deletion_protection enabled; clear it via Update before Delete", id)
	}
	// Адрес используется каким-либо referrer'ом (NIC, load balancer, …) —
	// блокируем до создания Operation. `used` держится в синхроне с referrer-row
	// через SetReference / ClearReference; читаем referrer ради точного сообщения.
	// Шаблон единый для любого типа referrer'а: сначала снять привязку у корневого
	// ресурса, потом удалять адрес.
	if existing.Used {
		ref, refErr := rd.Addresses().GetReference(ctx, id)
		_ = rd.Close()
		if refErr == nil && ref != nil {
			referrer := ref.ReferrerName
			if referrer == "" {
				referrer = ref.ReferrerID
			}
			return nil, status.Errorf(codes.FailedPrecondition,
				"address %s is in use by %s %s; detach it before deleting the address",
				id, referrerTypeLabel(ref.ReferrerType), referrer)
		}
		// Referrer-row нет (или чтение упало), но used=true — все равно блокируем generic-сообщением.
		return nil, status.Errorf(codes.FailedPrecondition, "address %s is in use", id)
	}
	_ = rd.Close()

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Delete address %s", id),
		&vpcv1.DeleteAddressMetadata{AddressId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		w, err := u.repo.Writer(ctx)
		if err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}
		defer w.Abort()

		// CAS-delete на СВЕЖЕМ состоянии (не sync-snapshot): удаляет только если
		// адрес все еще не used и не protected, возвращает удаленную строку. Это
		// атомарный backstop к sync-проверке выше — между sync и worker'ом адрес
		// мог быть приаттачен к NIC (used=true) или защищен; без CAS безусловный
		// DELETE молча каскадил бы address_references.
		deleted, err := w.Addresses().DeleteGuarded(ctx, id)
		if err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}

		// external_ipv6 / freelist-возврат вычисляем из УДАЛЕННОЙ строки (свежий
		// snapshot), а не из sync-read `existing` — иначе IP, аллоцированный
		// между sync-read и worker'ом, утек бы.
		if deleted.ExternalIpv6 != nil && deleted.ExternalIpv6.Address != "" {
			if frr := w.Addresses().FreeExternalIPv6(ctx, id); frr != nil {
				return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: free external ipv6: %v", repo.ErrInternal, frr))
			}
		}
		if deleted.ExternalIpv4 != nil && deleted.ExternalIpv4.Address != "" && deleted.ExternalIpv4.AddressPoolID != "" {
			if rerr := w.Addresses().ReturnIPToFreelist(ctx, deleted.ExternalIpv4.AddressPoolID, deleted.ExternalIpv4.Address); rerr != nil {
				return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: return ip to freelist: %v", repo.ErrInternal, rerr))
			}
		}
		if err := w.Outbox().Emit(ctx, "Address", id, "DELETED", map[string]any{"id": id}); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
		}
		// Снимаем owner-tuple vpc_address→project в той же
		// writer-TX (projectID берем из удаленной строки).
		if rerr := w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(
			fgaregister.ProjectHierarchy(deleted.ProjectID, "vpc_address", id),
		)); rerr != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga unregister intent: %v", repo.ErrInternal, rerr))
		}
		if err := w.Commit(); err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}
