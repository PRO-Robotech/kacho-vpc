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
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// UpdateInput — параметры для UpdateRouteTableUseCase.Execute.
type UpdateInput struct {
	RouteTableID string
	RouteTable   domain.RouteTable // несёт Name/Description/Labels/StaticRoutes
	UpdateMask   []string
}

// UpdateRouteTableUseCase — sync-валидация update_mask + значений, затем создание
// Operation + async update в worker'е.
type UpdateRouteTableUseCase struct {
	repo    RouteTableRepo
	opsRepo operations.Repo
}

// NewUpdateRouteTableUseCase создаёт UpdateRouteTableUseCase.
func NewUpdateRouteTableUseCase(repo RouteTableRepo, opsRepo operations.Repo) *UpdateRouteTableUseCase {
	return &UpdateRouteTableUseCase{repo: repo, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateRouteTableUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, in.RouteTableID); err != nil {
		return nil, err
	}
	if in.RouteTableID == "" {
		return nil, status.Error(codes.InvalidArgument, "route_table_id required")
	}
	if err := validateRouteTableUpdate(in); err != nil {
		return nil, err
	}

	op, err := operations.New(
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
	rec, err := u.repo.Get(ctx, in.RouteTableID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applyRouteTableMask(&rec.RouteTable, in)
	updated, err := u.repo.Update(ctx, &rec.RouteTable)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalRouteTableRecord(updated)
}

// validateRouteTableUpdate проверяет name/description/labels/static_routes в Update.
func validateRouteTableUpdate(in UpdateInput) error {
	// Hard-immutable поля.
	for _, field := range in.UpdateMask {
		switch field {
		case "network_id", "folder_id":
			return invalidArg(field, field+" is immutable after RouteTable.Create")
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
