// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"
	"fmt"

	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// updatablePoolFields — known-set мутабельных полей AddressPool.Update; единая
// FieldMask-дисциплина, общая для всех ресурсов сервиса.
var updatablePoolFields = map[string]struct{}{
	"name": {}, "description": {}, "labels": {},
	"is_default": {}, "selector_labels": {}, "selector_priority": {},
}

// UpdatePoolReq — частичное обновление; nil-пойнтеры/false-flags = no-op.
//
// CIDR-поля через Update не меняются: состав CIDR-блоков пула правится только
// через `:addCidrBlocks` / `:removeCidrBlocks` (parity с Subnet add/remove-cidr-
// blocks). Update остается для name / description / labels / is_default / selector_*.
type UpdatePoolReq struct {
	ID         string
	UpdateMask []string // snake_case field paths; empty → full-PATCH мутабельных полей
	// Значения мутабельных полей. Применяются только если поле в UpdateMask
	// (или mask пуст — full-PATCH). Immutable/unknown в mask → InvalidArgument.
	Name             string
	Description      string
	Labels           map[string]string
	IsDefault        bool
	SelectorLabels   map[string]string
	SelectorPriority int32
}

// UpdateAddressPoolUseCase — admin-only частичный Update.
//
// Get + mutate + Update + outbox-emit идут в одной writer-TX
// `kacho.Repository.Writer(ctx)` — атомарность гарантирована. CIDR-замена
// вынесена в отдельные RPC (:addCidrBlocks / :removeCidrBlocks), поэтому
// init v6-курсора здесь не нужен: v6-family появляется на пуле только через
// :addCidrBlocks.
type UpdateAddressPoolUseCase struct {
	repo Repo
}

// NewUpdateAddressPoolUseCase собирает use-case.
func NewUpdateAddressPoolUseCase(r Repo) *UpdateAddressPoolUseCase {
	return &UpdateAddressPoolUseCase{repo: r}
}

// Execute применяет частичное обновление.
func (u *UpdateAddressPoolUseCase) Execute(ctx context.Context, req UpdatePoolReq) (*kachorepo.AddressPoolRecord, error) {
	// FieldMask discipline: immutable в mask → InvalidArgument; unknown →
	// InvalidArgument; пустой mask → full-PATCH мутабельных полей (применяются ниже).
	for _, f := range req.UpdateMask {
		switch f {
		case "kind", "zone_id", "id", "created_at", "pool_id":
			return nil, serviceerr.InvalidArg(f, f+" is immutable after AddressPool.Create")
		case "cidr_blocks", "v4_cidr_blocks", "v6_cidr_blocks":
			return nil, serviceerr.InvalidArg(f, f+" is immutable via Update; use AddCidrBlocks/RemoveCidrBlocks")
		}
	}
	if err := corevalidate.UpdateMask("update_mask", req.UpdateMask, updatablePoolFields); err != nil {
		return nil, err
	}

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()

	curRec, err := w.AddressPools().Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	cur := curRec.AddressPool

	// Пустой mask → full-PATCH всех мутабельных полей.
	updates := req.UpdateMask
	if len(updates) == 0 {
		updates = []string{"name", "description", "labels", "is_default", "selector_labels", "selector_priority"}
	}
	for _, f := range updates {
		switch f {
		case "name":
			cur.Name = domain.RcNameVPC(req.Name)
		case "description":
			cur.Description = domain.RcDescription(req.Description)
		case "labels":
			cur.Labels = domain.LabelsFromMap(req.Labels)
		case "is_default":
			cur.IsDefault = req.IsDefault
		case "selector_labels":
			cur.SelectorLabels = domain.LabelsFromMap(req.SelectorLabels)
		case "selector_priority":
			cur.SelectorPriority = req.SelectorPriority
		}
	}
	// Post-mutation self-validation: применённая запись обязана быть валидной ДО
	// repo.Update. Невалидное значение (например bad name) → InvalidArgument,
	// writer-TX откатывается (defer Abort), ничего не записано.
	if err := serviceerr.FromValidation(cur.Validate()); err != nil {
		return nil, err
	}

	updated, err := w.AddressPools().Update(ctx, &cur)
	if err != nil {
		return nil, err
	}
	if err := w.Outbox().Emit(ctx, "AddressPool", updated.ID, "UPDATED",
		helpers.AddressPoolDomainPayload(&updated.AddressPool)); err != nil {
		return nil, fmt.Errorf("%w: outbox emit: %v", serviceerr.ErrInternal, err)
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}
