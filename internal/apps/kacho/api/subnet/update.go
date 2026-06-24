// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

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

// UpdateInput — параметры для UpdateSubnetUseCase.Execute. Update'у нужны и
// domain.Subnet (с заявленными полями), и UpdateMask, поэтому используется
// отдельный input-тип.
type UpdateInput struct {
	SubnetID     string
	Subnet       domain.Subnet // несет Name/Description/Labels/RouteTableID/DhcpOptions/V4CidrBlocks; остальные не используются
	V4CidrBlocks []string      // soft-immutable: принимаем, но не пишем
	UpdateMask   []string
}

// UpdateSubnetUseCase — sync-валидация update_mask + значений, затем создание
// Operation + async update в worker'е.
//
// network_id / zone_id — hard-immutable: явное указание в update_mask →
// InvalidArgument; присланное в body без mask — silently игнорируется
// (full-object PATCH из UI). v4_cidr_blocks / v6_cidr_blocks в mask запрос не
// отвергают, но репозиторный Update не перезаписывает CIDR-колонки (defensive
// depth) — т.е. изменение CIDR через Update это no-op; правят их через
// :add/:removeCidrBlocks.
//
// Worker открывает Writer-TX и делает Get+Update+outbox атомарно.
type UpdateSubnetUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateSubnetUseCase создает UpdateSubnetUseCase.
func NewUpdateSubnetUseCase(r Repo, opsRepo operations.Repo) *UpdateSubnetUseCase {
	return &UpdateSubnetUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateSubnetUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, in.SubnetID); err != nil {
		return nil, err
	}
	if in.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "network_id", "zone_id":
			return nil, serviceerr.InvalidArg(field, field+" is immutable after Subnet.Create")
		}
	}
	if err := serviceerr.FromValidation(validateSubnetUpdate(in)); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update subnet %s", in.SubnetID),
		&vpcv1.UpdateSubnetMetadata{SubnetId: in.SubnetID},
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

func (u *UpdateSubnetUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
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
	rec, err := w.Subnets().GetForUpdate(ctx, in.SubnetID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	applySubnetMask(&rec.Subnet, in)
	updated, err := w.Subnets().Update(ctx, &rec.Subnet)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Subnet", updated.ID, "UPDATED", helpers.DomainToMap(updated)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	// Если labels были в update_mask (или это full-object PATCH), пере-эмитим
	// register-intent с обновленными labels в ТОЙ ЖЕ writer-TX, чтобы kacho-iam
	// держал resource_mirror актуальным для label-селектора (reconcile при смене
	// label'ов). Update без labels → re-emit не делаем.
	if labelsInMask(in.UpdateMask) {
		if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
			fgaregister.ProjectHierarchyItem(string(updated.ProjectID), "vpc_subnet", updated.ID,
				domain.LabelsToMap(updated.Labels)),
		)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return marshalSubnetRecord(updated)
}

// validateSubnetUpdate проверяет name/description/labels/dhcp_options в Update.
// Валидация идет через domain-newtypes (self-validating domain).
//
// Immutable-поля (v4_cidr_blocks, v6_cidr_blocks, network_id, zone_id) известны
// маску-валидатору (чтобы пройти check на unknown), а сама immutability ловится
// выше в Execute() (network_id/zone_id) либо игнорируется silently (v4/v6_cidr).
func validateSubnetUpdate(in UpdateInput) error {
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {},
		"route_table_id": {}, "dhcp_options": {},
		"v4_cidr_blocks": {}, "v6_cidr_blocks": {}, "network_id": {}, "zone_id": {},
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
			// Subnet: пустое name допустимо (разрешительная политика валидации).
			if err := in.Subnet.Name.Validate(); err != nil {
				return err
			}
		case "description":
			if err := in.Subnet.Description.Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(in.Subnet.Labels); err != nil {
				return err
			}
		case "dhcp_options":
			if err := validateDhcpOptions(in.Subnet.DhcpOptions); err != nil {
				return err
			}
		}
	}
	// Полный апдейт (без update_mask) — DhcpOptions тоже валидируются.
	if len(in.UpdateMask) == 0 {
		if err := validateDhcpOptions(in.Subnet.DhcpOptions); err != nil {
			return err
		}
	}
	return nil
}

// labelsInMask сообщает, затрагивает ли update_mask поле labels: пустая маска —
// это full-object PATCH (labels применяются), явная маска совпадает, если в ней
// есть "labels". Управляет re-emit register-intent — держать в синхроне с набором
// full-PATCH-полей applySubnetMask.
func labelsInMask(updateMask []string) bool {
	if len(updateMask) == 0 {
		return true // full-object PATCH пишет labels
	}
	for _, f := range updateMask {
		if f == "labels" {
			return true
		}
	}
	return false
}

// applySubnetMask применяет mutable-поля из in к sub.
//
// Immutable-поля (v4_cidr_blocks, v6_cidr_blocks, network_id, zone_id) НЕ
// применяются никогда — даже если клиент прислал их в body без mask. Sync-check
// в Execute() уже отверг бы попытку явно указать их в update_mask
// (network_id/zone_id) или silently-игнор для v4/v6_cidr_blocks.
func applySubnetMask(sub *domain.Subnet, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		// Полный update — только mutable-поля.
		sub.Name = in.Subnet.Name
		sub.Description = in.Subnet.Description
		sub.Labels = in.Subnet.Labels
		sub.RouteTableID = in.Subnet.RouteTableID
		sub.DhcpOptions = in.Subnet.DhcpOptions
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			sub.Name = in.Subnet.Name
		case "description":
			sub.Description = in.Subnet.Description
		case "labels":
			sub.Labels = in.Subnet.Labels
		case "route_table_id":
			sub.RouteTableID = in.Subnet.RouteTableID
		case "dhcp_options":
			sub.DhcpOptions = in.Subnet.DhcpOptions
		}
	}
}
