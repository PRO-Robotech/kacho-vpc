// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// updatableFields — known-set мутабельных полей AnycastAddressPool.Update
// (единая FieldMask-дисциплина). scope/ip_version/cidr_blocks/is_default —
// immutable после Create.
var updatableFields = map[string]struct{}{
	"name": {}, "description": {}, "labels": {},
}

// UpdateInput — частичное обновление. Пустой mask → full-PATCH мутабельных полей.
type UpdateInput struct {
	ID          string
	UpdateMask  []string
	Name        string
	Description string
	Labels      map[string]string
}

// UpdateAnycastAddressPoolUseCase — async Update мутабельных полей через Operation.
// FieldMask-дисциплина (immutable/unknown) — sync (fast-fail). Сам апдейт + outbox
// — атомарно в writer-TX worker'а.
type UpdateAnycastAddressPoolUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateAnycastAddressPoolUseCase создаёт use-case.
func NewUpdateAnycastAddressPoolUseCase(r Repo, opsRepo operations.Repo) *UpdateAnycastAddressPoolUseCase {
	return &UpdateAnycastAddressPoolUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync mask-валидация + create Operation + worker.
func (u *UpdateAnycastAddressPoolUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	// FieldMask discipline: immutable в mask → InvalidArgument; unknown →
	// InvalidArgument; пустой mask → full-PATCH мутабельных полей (применяются ниже).
	for _, f := range in.UpdateMask {
		switch f {
		case "cidr_blocks", "scope", "ip_version", "is_default", "id", "project_id", "created_at", "status":
			return nil, serviceerr.InvalidArg(f, f+" is immutable after AnycastAddressPool.Create")
		}
	}
	if err := corevalidate.UpdateMask("update_mask", in.UpdateMask, updatableFields); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update anycast address pool %s", in.ID),
		&vpcv1.UpdateAnycastAddressPoolMetadata{AnycastAddressPoolId: in.ID},
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

// doUpdate — async-часть: Get + mutate + Update + outbox в одной writer-TX.
func (u *UpdateAnycastAddressPoolUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	curRec, err := w.AnycastAddressPools().Get(ctx, in.ID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	cur := curRec.AnycastAddressPool

	updates := in.UpdateMask
	if len(updates) == 0 {
		updates = []string{"name", "description", "labels"}
	}
	for _, f := range updates {
		switch f {
		case "name":
			cur.Name = domain.RcNameVPC(in.Name)
		case "description":
			cur.Description = domain.RcDescription(in.Description)
		case "labels":
			cur.Labels = domain.LabelsFromMap(in.Labels)
		}
	}
	if err := serviceerr.FromValidation(cur.Validate()); err != nil {
		return nil, err
	}

	updated, err := w.AnycastAddressPools().Update(ctx, &cur)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "AnycastAddressPool", updated.ID, "UPDATED",
		helpers.DomainToMap(updated.AnycastAddressPool)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return anypb.New(toProto(updated))
}
