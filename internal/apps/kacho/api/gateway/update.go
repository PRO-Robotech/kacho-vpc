// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package gateway

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// UpdateInput — параметры для UpdateGatewayUseCase.Execute. Для Update нужны и
// domain.Gateway (с заявленными полями), и ортогональный ему UpdateMask.
type UpdateInput struct {
	GatewayID  string
	Gateway    domain.Gateway // несет Name/Description/Labels/GatewayType; остальные поля не используются
	UpdateMask []string
}

// UpdateGatewayUseCase — sync-валидация update_mask и значений, затем создание
// Operation и async-update в worker'е. doUpdate открывает Writer-TX и делает
// Get + apply mask + Update + outbox emit в одной транзакции.
type UpdateGatewayUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateGatewayUseCase создает UpdateGatewayUseCase.
func NewUpdateGatewayUseCase(r Repo, opsRepo operations.Repo) *UpdateGatewayUseCase {
	return &UpdateGatewayUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateGatewayUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, in.GatewayID); err != nil {
		return nil, err
	}
	if in.GatewayID == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	if err := serviceerr.FromValidation(validateGatewayUpdate(in)); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update gateway %s", in.GatewayID),
		&vpcv1.UpdateGatewayMetadata{GatewayId: in.GatewayID},
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

func (u *UpdateGatewayUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// GetForUpdate (SELECT … FOR UPDATE) + Update в одной writer-TX: row-lock
	// сериализует read-modify-write. Конкурентный Update с disjoint update_mask
	// блокируется на GetForUpdate до commit первого, затем читает уже обновленный
	// row и применяет свою маску поверх — lost-update исключен. Plain Get здесь
	// был бы race-prone (second-writer-wins).
	rec, err := w.Gateways().GetForUpdate(ctx, in.GatewayID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	applyGatewayMask(&rec.Gateway, in)
	updated, err := w.Gateways().Update(ctx, &rec.Gateway)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "Gateway", updated.ID, "UPDATED", helpers.DomainToMap(updated)); oerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	// Если labels попали в update_mask (или это full-object PATCH), переэмитим
	// register-intent с обновленными метками в ТОЙ ЖЕ writer-TX, чтобы kacho-iam
	// держал resource_mirror в актуальном виде для ARM_LABELS-селектора (revoke
	// при снятии метки). Update без labels → переэмита нет. Полное снятие labels →
	// upsert с пустыми метками (НЕ Unregister: Gateway все еще существует). Эталон —
	// network/subnet/securitygroup update.
	if labelsInMask(in.UpdateMask) {
		if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
			fgaregister.ProjectHierarchyItem(string(updated.ProjectID), "vpc_gateway", updated.ID,
				domain.LabelsToMap(updated.Labels)),
		)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return marshalGatewayRecord(updated)
}

// labelsInMask — затрагивает ли update_mask поле `labels`: пустая маска значит
// full-object PATCH (labels применяются), явная маска матчится, если содержит
// "labels". Управляет переэмитом register-intent — держать в синхроне с
// full-PATCH-набором полей в applyGatewayMask.
func labelsInMask(updateMask []string) bool {
	if len(updateMask) == 0 {
		return true // full-object PATCH writes labels
	}
	for _, f := range updateMask {
		if f == "labels" {
			return true
		}
	}
	return false
}

// validateGatewayUpdate — sync-проверка update_mask и значений: description/labels
// валидируются через domain newtype.Validate(), name — через
// corevalidate.NameGateway (strict-name).
func validateGatewayUpdate(in UpdateInput) error {
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}, "gateway_type": {}}
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
			if err := corevalidate.NameGateway("name", string(in.Gateway.Name)); err != nil {
				return err
			}
		case "description":
			if err := in.Gateway.Description.Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(in.Gateway.Labels); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyGatewayMask — применяет subset полей к существующему domain.Gateway.
// Пустой mask = full-object PATCH.
func applyGatewayMask(g *domain.Gateway, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		g.Name = in.Gateway.Name
		g.Description = in.Gateway.Description
		g.Labels = in.Gateway.Labels
		if in.Gateway.GatewayType != "" {
			g.GatewayType = in.Gateway.GatewayType
		}
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			g.Name = in.Gateway.Name
		case "description":
			g.Description = in.Gateway.Description
		case "labels":
			g.Labels = in.Gateway.Labels
		case "gateway_type":
			if in.Gateway.GatewayType != "" {
				g.GatewayType = in.Gateway.GatewayType
			}
		}
	}
}
