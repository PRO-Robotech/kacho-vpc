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
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// UpdateInput — параметры для UpdateGatewayUseCase.Execute. Конкретно для Update
// нужны и domain.Gateway (с заявленными полями), и UpdateMask (skill evgeniy
// §7 I.1 — XxxReq плох, если зеркалит domain; в нашем случае orthogonal mask).
type UpdateInput struct {
	GatewayID  string
	Gateway    domain.Gateway // несёт Name/Description/Labels/GatewayType; остальные поля не используются
	UpdateMask []string
}

// UpdateGatewayUseCase — sync-валидация update_mask + значений, затем создание
// Operation + async update в worker'е.
type UpdateGatewayUseCase struct {
	repo    GatewayRepo
	opsRepo operations.Repo
}

// NewUpdateGatewayUseCase создаёт UpdateGatewayUseCase.
func NewUpdateGatewayUseCase(repo GatewayRepo, opsRepo operations.Repo) *UpdateGatewayUseCase {
	return &UpdateGatewayUseCase{repo: repo, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateGatewayUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, in.GatewayID); err != nil {
		return nil, err
	}
	if in.GatewayID == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id required")
	}
	if err := validateGatewayUpdate(in); err != nil {
		return nil, err
	}

	op, err := operations.New(
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
	rec, err := u.repo.Get(ctx, in.GatewayID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applyGatewayMask(&rec.Gateway, in)
	updated, err := u.repo.Update(ctx, &rec.Gateway)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalGatewayRecord(updated)
}

// validateGatewayUpdate — sync-проверка update_mask и значений.
//
// Wave 2 batch B (KAC-94): description/labels — через domain newtype.Validate().
// Name по-прежнему через corevalidate.NameGateway (strict-name).
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
// no-mask = full PATCH (verbatim YC).
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
