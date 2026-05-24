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
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// UpdateInput — параметры для UpdateSubnetUseCase.Execute. Конкретно для Update
// нам нужен и domain.Subnet (с заявленными полями), и UpdateMask. Поэтому
// небольшой собственный input-тип допустим (skill evgeniy §7 I.1).
type UpdateInput struct {
	SubnetID     string
	Subnet       domain.Subnet // несёт Name/Description/Labels/RouteTableID/DhcpOptions/V4CidrBlocks; остальные не используются
	V4CidrBlocks []string      // soft-immutable; принимаем но не пишем (verbatim YC)
	UpdateMask   []string
}

// UpdateSubnetUseCase — sync-валидация update_mask + значений, затем создание
// Operation + async update в worker'е.
//
// SU-CIDR-IM-1: network_id / zone_id — hard-immutable: явное указание в
// update_mask → InvalidArgument; присланное в body без mask — silently
// игнорируется (full-object PATCH UI). v4_cidr_blocks / v6_cidr_blocks —
// verbatim YC (probe 2026-05-11, kacho-vpc#10) НЕ отвергает их в mask: YC
// принимает запрос (200). Мы тоже принимаем — но репозиторный Update не
// перезаписывает CIDR-колонки (defensive depth), т.е. изменение CIDR через
// Update — no-op (документировано в 07-known-divergences.md).
//
// Wave 5 replicate (KAC-94): worker открывает Writer-TX, делает Get+Update+outbox
// атомарно.
type UpdateSubnetUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateSubnetUseCase создаёт UpdateSubnetUseCase.
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
			return nil, invalidArg(field, field+" is immutable after Subnet.Create")
		}
	}
	if err := validateSubnetUpdate(in); err != nil {
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
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	// Get + Update внутри одной writer-TX: race-free read-modify-write.
	rec, err := w.Subnets().Get(ctx, in.SubnetID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applySubnetMask(&rec.Subnet, in)
	updated, err := w.Subnets().Update(ctx, &rec.Subnet)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Subnet", updated.ID, "UPDATED", subnetPayloadMap(updated)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalSubnetRecord(updated)
}

// validateSubnetUpdate проверяет name/description/labels/dhcp_options в Update.
// Skill evgeniy §4 D.5 / AP-1: валидация через domain newtypes (corevalidate.NameVPC/
// Description/Labels удалены).
//
// Immutable-поля (v4_cidr_blocks, v6_cidr_blocks, network_id, zone_id) — известны
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
			// VPC Subnet: empty name allowed (YC permissive policy).
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

// applySubnetMask применяет mutable поля из in к sub.
//
// Immutable fields (v4_cidr_blocks, v6_cidr_blocks, network_id, zone_id) НЕ
// применяются никогда — даже если клиент прислал их в body без mask. Sync-check
// в Execute() уже отверг бы попытку явно указать их в update_mask
// (network_id/zone_id) или silently-игнор для v4/v6_cidr_blocks.
func applySubnetMask(sub *domain.Subnet, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		// Полный update — только mutable fields.
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
