// Package service — internal Network operations not expressible через public Update.
//
// Используется через kacho.cloud.vpc.v1.InternalNetworkService gRPC.
package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkInternal — internal-only operations над Network (computed-fields).
type NetworkInternal struct {
	repo NetworkRepo
	sgs  SecurityGroupRepo
}

func NewNetworkInternal(repo NetworkRepo, sgs SecurityGroupRepo) *NetworkInternal {
	return &NetworkInternal{repo: repo, sgs: sgs}
}

// SetDefaultSecurityGroupId — выставляет computed-поле
// Network.default_security_group_id. Public Update API не принимает это
// поле в UpdateMask (immutable / output-only по convention).
//
// Idempotent: повторный вызов с тем же sg_id — no-op.
// FailedPrecondition если уже задан другой sg_id (защита от случайного
// перезаписывания).
func (s *NetworkInternal) SetDefaultSecurityGroupId(ctx context.Context, networkID, sgID string) error {
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

var _ domain.AddressPool // silence unused-import in case domain not referenced elsewhere
