package network

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// UpdateInput — параметры для UpdateNetworkUseCase.Execute. Конкретно для Update
// нам нужен и domain.Network (с заявленными полями), и UpdateMask. Поэтому
// небольшой собственный input-тип допустим (skill evgeniy §7 I.1 — XxxReq плох,
// если зеркалит domain; в нашем случае мы храним domain плюс orthogonal mask).
type UpdateInput struct {
	NetworkID  string
	Network    domain.Network // несёт Name/Description/Labels, остальные поля не используются
	UpdateMask []string
}

// UpdateNetworkUseCase — sync-валидация update_mask + значений, затем создание
// Operation + async update в worker'е.
//
// Wave 5 pilot (KAC-94): writer-TX явный, DML + outbox atomic.
type UpdateNetworkUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateNetworkUseCase создаёт UpdateNetworkUseCase.
func NewUpdateNetworkUseCase(r Repo, opsRepo operations.Repo) *UpdateNetworkUseCase {
	return &UpdateNetworkUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateNetworkUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, in.NetworkID); err != nil {
		return nil, err
	}
	if in.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := validateNetworkUpdate(in); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update network %s", in.NetworkID),
		&vpcv1.UpdateNetworkMetadata{NetworkId: in.NetworkID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doUpdate(ctx, in)
	})

	return &op, nil
}

// doUpdate — async worker-тело Network.Update: применяет mutable-поля в writer-TX.
func (u *UpdateNetworkUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	// Get + Update внутри одной writer-TX: race-free read-modify-write.
	rec, err := w.Networks().Get(ctx, in.NetworkID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applyNetworkMask(&rec.Network, in)
	updated, err := w.Networks().Update(ctx, &rec.Network)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", updated.ID, "UPDATED", networkPayloadMap(updated)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalNetworkRecord(updated)
}

// validateNetworkUpdate — sync-проверка update_mask и значений. Skill evgeniy
// §4 D.5 / AP-1: преобразуем заявленные поля в domain-newtypes и зовём их
// `Validate()` напрямую (corevalidate.* отсутствует).
func validateNetworkUpdate(in UpdateInput) error {
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}}
	if err := corevalidate.UpdateMask("update_mask", in.UpdateMask, known); err != nil {
		return err
	}
	updates := in.UpdateMask
	if len(updates) == 0 {
		updates = []string{"name", "description", "labels"}
	}
	for _, f := range updates {
		switch f {
		case "name":
			if err := in.Network.Name.Validate(); err != nil {
				return err
			}
		case "description":
			if err := in.Network.Description.Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(in.Network.Labels); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyNetworkMask — применяет subset полей к существующему domain.Network.
// no-mask = full PATCH (verbatim YC).
func applyNetworkMask(n *domain.Network, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		n.Name = in.Network.Name
		n.Description = in.Network.Description
		n.Labels = in.Network.Labels
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			n.Name = in.Network.Name
		case "description":
			n.Description = in.Network.Description
		case "labels":
			n.Labels = in.Network.Labels
		}
	}
}
