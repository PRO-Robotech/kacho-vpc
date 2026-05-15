package subnet

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
)

// RelocateUseCase — перенос Subnet в другую zone.
//
// Verbatim YC (probe 2026-05-11, kacho-vpc#10): Relocate ВСЕГДА отвергается
// синхронно с FailedPrecondition "Invalid subnet state" — даже для свежей
// подсети без адресов и валидной целевой зоны. YC требует какое-то внутреннее
// состояние подсети, которое control-plane без data-plane не моделирует
// (multi-zone network?). Поэтому Operation не создаётся: после format-check id,
// валидации destination_zone_id и проверки существования подсети →
// FAILED_PRECONDITION "Invalid subnet state".
type RelocateUseCase struct {
	repo    SubnetRepo
	zoneReg ZoneRegistry
}

// NewRelocateUseCase создаёт RelocateUseCase.
func NewRelocateUseCase(repo SubnetRepo, zoneReg ZoneRegistry) *RelocateUseCase {
	return &RelocateUseCase{repo: repo, zoneReg: zoneReg}
}

// Execute — синхронные precondition'ы → ВСЕГДА FailedPrecondition.
// Возвращаемый Operation всегда nil — Operation не создаётся.
func (u *RelocateUseCase) Execute(ctx context.Context, id, destZoneID string) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if err := validateZoneID(ctx, u.zoneReg, "destination_zone_id", destZoneID); err != nil {
		return nil, err
	}
	if _, err := u.repo.Get(ctx, id); err != nil {
		return nil, mapRepoErr(err)
	}
	return nil, status.Error(codes.FailedPrecondition, "Invalid subnet state")
}
