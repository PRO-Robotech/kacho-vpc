package address

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// DeleteAddressUseCase — sync FAILED_PRECONDITION при deletion_protection=true
// или при «адрес в использовании» (referrer-row). Async-часть (worker):
// освобождение external_ipv6 → DELETE address → return v4 IP в freelist.
type DeleteAddressUseCase struct {
	repo    AddressRepo
	opsRepo operations.Repo
}

// NewDeleteAddressUseCase создаёт DeleteAddressUseCase.
func NewDeleteAddressUseCase(repo AddressRepo, opsRepo operations.Repo) *DeleteAddressUseCase {
	return &DeleteAddressUseCase{repo: repo, opsRepo: opsRepo}
}

// Execute инициирует Delete: sync-проверки → Operation → worker.
func (u *DeleteAddressUseCase) Execute(ctx context.Context, id string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}

	existing, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if existing.DeletionProtection {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has deletion_protection enabled; clear it via Update before Delete", id)
	}
	// Address in use by NIC (or any referrer) — block before the Operation
	// (KAC-31). `used` is kept in sync with the referrer-row by SetReference /
	// ClearReference; read the referrer for a precise message.
	if existing.Used {
		ref, refErr := u.repo.GetReference(ctx, id)
		switch {
		case refErr == nil && ref != nil && ref.ReferrerType == niReferrerType:
			referrer := ref.ReferrerName
			if referrer == "" {
				referrer = ref.ReferrerID
			}
			return nil, status.Errorf(codes.FailedPrecondition,
				"address %s is in use by network interface %s; detach it before deleting the address", id, referrer)
		case refErr == nil && ref != nil:
			return nil, status.Errorf(codes.FailedPrecondition, "address %s is in use", id)
		default:
			// No referrer row (or read failed) but used=true — still block generically.
			return nil, status.Errorf(codes.FailedPrecondition, "address %s is in use", id)
		}
	}

	op, err := operations.New(
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

	// Capture any allocated external IP before delete — мы вернём его в
	// address_pool_free_ips после успешного DELETE, чтобы освобождённый IP
	// сразу попал обратно в оборот PG-native allocator'а (миграция 0014).
	var (
		allocatedIP, allocatedPoolID string
		hasExternalIPv6              bool
	)
	if existing.ExternalIpv4 != nil {
		allocatedIP = existing.ExternalIpv4.Address
		allocatedPoolID = existing.ExternalIpv4.AddressPoolID
	}
	if existing.ExternalIpv6 != nil && existing.ExternalIpv6.Address != "" {
		hasExternalIPv6 = true
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		// KAC-60: освобождаем external_ipv6 ДО Delete address (FK
		// ipv6_allocated_ips.address_id ссылается на addresses.id неявно через
		// service-логику; FreeExternalIPv6 идемпотентен, no-op если уже free).
		if hasExternalIPv6 {
			if frr := u.repo.FreeExternalIPv6(ctx, id); frr != nil {
				slog.WarnContext(ctx, "address delete: failed to free external ipv6 (continuing)",
					"address_id", id, "err", frr)
			}
		}
		if err := u.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		// Best-effort return-to-freelist. Failure здесь не валит Delete —
		// адрес уже удалён, IP в худшем случае просто «осядет» (recoverable
		// через admin-tooling backfill). Иначе сбой return'а сделал бы
		// Operation failed после фактического удаления — клиент увидел бы
		// inconsistent state.
		if allocatedIP != "" && allocatedPoolID != "" {
			if rerr := u.repo.ReturnIPToFreelist(ctx, allocatedPoolID, allocatedIP); rerr != nil {
				slog.WarnContext(ctx, "address delete: failed to return IP to freelist",
					"address_id", id, "pool_id", allocatedPoolID, "ip", allocatedIP, "err", rerr)
			}
		}
		return anypb.New(&emptypb.Empty{})
	})

	return &op, nil
}
