// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package routetable

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// UpdateInput — параметры для UpdateRouteTableUseCase.Execute.
type UpdateInput struct {
	RouteTableID string
	RouteTable   domain.RouteTable // несет Name/Description/Labels/StaticRoutes
	UpdateMask   []string
}

// UpdateRouteTableUseCase — sync-валидация update_mask и значений, затем создание
// Operation и async-апдейт в worker'е. Writer-TX явный: DML и outbox-emit атомарны.
type UpdateRouteTableUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateRouteTableUseCase создает UpdateRouteTableUseCase.
func NewUpdateRouteTableUseCase(r Repo, opsRepo operations.Repo) *UpdateRouteTableUseCase {
	return &UpdateRouteTableUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateRouteTableUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, in.RouteTableID); err != nil {
		return nil, err
	}
	if in.RouteTableID == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	if err := serviceerr.FromValidation(validateRouteTableUpdate(in)); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update route table %s", in.RouteTableID),
		&vpcv1.UpdateRouteTableMetadata{RouteTableId: in.RouteTableID},
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

func (u *UpdateRouteTableUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// GetForUpdate (SELECT … FOR UPDATE) + Update в одной writer-TX: row-lock
	// сериализует read-modify-write. Конкурентный Update с disjoint update_mask
	// блокируется на GetForUpdate до commit первого, затем читает уже обновленный
	// row и применяет свою маску поверх — lost-update исключен. Plain Get здесь был
	// бы race-prone (second-writer-wins).
	rec, err := w.RouteTables().GetForUpdate(ctx, in.RouteTableID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	applyRouteTableMask(&rec.RouteTable, in)
	updated, err := w.RouteTables().Update(ctx, &rec.RouteTable)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "RouteTable", updated.ID, "UPDATED", helpers.RouteTablePayload(updated)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	// Если labels попали в update_mask (или это full-object PATCH), переэмитим
	// register-intent с обновленными метками в ТОЙ ЖЕ writer-TX, чтобы kacho-iam
	// держал resource_mirror в актуальном виде для ARM_LABELS-селектора (revoke
	// при снятии метки). Update без labels → переэмита нет. Полное снятие labels →
	// upsert с пустыми метками (НЕ Unregister: RouteTable все еще существует,
	// mirror-row остается, просто перестает матчиться селектором). Эталон —
	// network/subnet/securitygroup update.
	if labelsInMask(in.UpdateMask) {
		if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
			fgaregister.ProjectHierarchyItem(string(updated.ProjectID), "vpc_route_table", updated.ID,
				domain.LabelsToMap(updated.Labels)),
		)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return marshalRouteTableRecord(updated)
}

// labelsInMask — затрагивает ли update_mask поле `labels`: пустая маска значит
// full-object PATCH (labels применяются), явная маска матчится, если содержит
// "labels". Управляет переэмитом register-intent — держать в синхроне с
// full-PATCH-набором полей в applyRouteTableMask.
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

// validateRouteTableUpdate проверяет name/description/labels/static_routes в Update.
func validateRouteTableUpdate(in UpdateInput) error {
	// Hard-immutable поля.
	for _, field := range in.UpdateMask {
		switch field {
		case "network_id", "project_id":
			return serviceerr.InvalidArg(field, field+" is immutable after RouteTable.Create")
		}
	}
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {}, "static_routes": {},
	}
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
			if err := in.RouteTable.Name.Validate(); err != nil {
				return err
			}
		case "description":
			if err := in.RouteTable.Description.Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(in.RouteTable.Labels); err != nil {
				return err
			}
		case "static_routes":
			if err := validateStaticRoutes(in.RouteTable.StaticRoutes); err != nil {
				return err
			}
		}
	}
	// Полный апдейт без mask тоже валидирует static_routes, если они есть.
	if len(in.UpdateMask) == 0 && len(in.RouteTable.StaticRoutes) > 0 {
		if err := validateStaticRoutes(in.RouteTable.StaticRoutes); err != nil {
			return err
		}
	}
	return nil
}

// applyRouteTableMask — применяет subset полей к существующему domain.RouteTable.
func applyRouteTableMask(rt *domain.RouteTable, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		rt.Name = in.RouteTable.Name
		rt.Description = in.RouteTable.Description
		rt.Labels = in.RouteTable.Labels
		rt.StaticRoutes = in.RouteTable.StaticRoutes
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			rt.Name = in.RouteTable.Name
		case "description":
			rt.Description = in.RouteTable.Description
		case "labels":
			rt.Labels = in.RouteTable.Labels
		case "static_routes":
			rt.StaticRoutes = in.RouteTable.StaticRoutes
		}
	}
}
