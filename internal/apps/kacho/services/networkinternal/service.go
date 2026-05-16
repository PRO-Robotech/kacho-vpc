// Package networkinternal — internal Network operations not expressible через
// public Update.
//
// Используется через kacho.cloud.vpc.v1.InternalNetworkService gRPC.
//
// Wave 3 cleanup (KAC-94): перенесено из `internal/service/network_internal.go`
// в `internal/apps/kacho/services/networkinternal/` согласно skill evgeniy §1 A.3 —
// это не-resource service (computed-field управление сетью; ресурс-CRUD живёт
// в `internal/apps/kacho/api/network/`).
package networkinternal

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// NetworkRepo — узкий port-интерфейс над repo.NetworkRepo: только методы,
// нужные для SetDefaultSecurityGroupId.
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*domain.NetworkRecord, error)
	Update(ctx context.Context, n *domain.Network) (*domain.NetworkRecord, error)
}

// SecurityGroupRepo — узкий port-интерфейс над repo.SecurityGroupRepo: только
// Get (для FK-валидации SG→Network).
type SecurityGroupRepo interface {
	Get(ctx context.Context, id string) (*domain.SecurityGroupRecord, error)
}

// Service — internal-only operations над Network (computed-fields).
//
// Раньше тип назывался `NetworkInternal` — переименован к `Service`
// (skill evgeniy §3 C.3: имя типа дублирует имя пакета —
// `networkinternal.Service`).
type Service struct {
	repo NetworkRepo
	sgs  SecurityGroupRepo
}

// NewService создаёт Service.
func NewService(repo NetworkRepo, sgs SecurityGroupRepo) *Service {
	return &Service{repo: repo, sgs: sgs}
}

// SetDefaultSecurityGroupId — выставляет computed-поле
// Network.default_security_group_id. Public Update API не принимает это
// поле в UpdateMask (immutable / output-only по convention).
//
// Idempotent: повторный вызов с тем же sg_id — no-op.
// FailedPrecondition если уже задан другой sg_id (защита от случайного
// перезаписывания).
func (s *Service) SetDefaultSecurityGroupId(ctx context.Context, networkID, sgID string) error {
	n, err := s.repo.Get(ctx, networkID)
	if err != nil {
		return err
	}
	if n.DefaultSecurityGroupID == sgID {
		return nil // idempotent
	}
	if n.DefaultSecurityGroupID != "" {
		return status.Errorf(codes.FailedPrecondition,
			"network %s already has default_security_group_id=%s; refusing to overwrite with %s",
			networkID, n.DefaultSecurityGroupID, sgID)
	}
	// Validate FK: sg должна существовать и принадлежать этой network.
	sg, err := s.sgs.Get(ctx, sgID)
	if err != nil {
		return err
	}
	if sg.NetworkID != networkID {
		return status.Errorf(codes.InvalidArgument,
			"security group %s belongs to network %s, not %s",
			sgID, sg.NetworkID, networkID)
	}
	n.DefaultSecurityGroupID = sgID
	// Update принимает domain.Network (без CreatedAt) — берём embedded.
	_, err = s.repo.Update(ctx, &n.Network)
	return err
}

// ports-import-keeper: ports re-export'ит sentinel-ошибки (ErrNotFound и т.п.),
// которые приходят от repo.Get/Update и пробрасываются вверх как есть.
var _ = repo.ErrNotFound
