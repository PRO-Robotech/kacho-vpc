package privateendpoint

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	pe "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// UpdateInput — параметры для UpdatePrivateEndpointUseCase.Execute.
type UpdateInput struct {
	PrivateEndpointID string
	PrivateEndpoint   domain.PrivateEndpoint // несёт Name/Description/Labels/DnsOptions
	UpdateMask        []string
}

// UpdatePrivateEndpointUseCase — sync-валидация update_mask + значений, затем
// async update в worker'е.
//
// Wave 5 replicate (KAC-94): writer-TX явный, DML + outbox atomic (parity с
// Network).
type UpdatePrivateEndpointUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdatePrivateEndpointUseCase создаёт UpdatePrivateEndpointUseCase.
func NewUpdatePrivateEndpointUseCase(r Repo, opsRepo operations.Repo) *UpdatePrivateEndpointUseCase {
	return &UpdatePrivateEndpointUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdatePrivateEndpointUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("private endpoint", ids.PrefixPrivateEndpoint, in.PrivateEndpointID); err != nil {
		return nil, err
	}
	if in.PrivateEndpointID == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	if err := validatePrivateEndpointUpdate(in); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update private endpoint %s", in.PrivateEndpointID),
		&pe.UpdatePrivateEndpointMetadata{PrivateEndpointId: in.PrivateEndpointID},
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

// doUpdate — async-часть Update. Get + Update в одной writer-TX (race-free
// read-modify-write); outbox.Emit("PrivateEndpoint", …, "UPDATED") в той же TX
// (skill evgeniy §6 G.5).
func (u *UpdatePrivateEndpointUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	rec, err := w.PrivateEndpoints().Get(ctx, in.PrivateEndpointID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applyPrivateEndpointMask(&rec.PrivateEndpoint, in)
	updated, err := w.PrivateEndpoints().Update(ctx, &rec.PrivateEndpoint)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "PrivateEndpoint", updated.ID, "UPDATED", privateEndpointPayloadMap(updated)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalPrivateEndpointRecord(updated)
}

// validatePrivateEndpointUpdate — sync-валидация update_mask.
func validatePrivateEndpointUpdate(in UpdateInput) error {
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}, "dns_options": {}}
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
			if err := in.PrivateEndpoint.Name.Validate(); err != nil {
				return err
			}
		case "description":
			if err := in.PrivateEndpoint.Description.Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(in.PrivateEndpoint.Labels); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyPrivateEndpointMask — применяет subset полей к существующему domain.PE.
func applyPrivateEndpointMask(p *domain.PrivateEndpoint, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		p.Name = in.PrivateEndpoint.Name
		p.Description = in.PrivateEndpoint.Description
		p.Labels = in.PrivateEndpoint.Labels
		if in.PrivateEndpoint.DnsOptions != nil {
			p.DnsOptions = in.PrivateEndpoint.DnsOptions
		}
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			p.Name = in.PrivateEndpoint.Name
		case "description":
			p.Description = in.PrivateEndpoint.Description
		case "labels":
			p.Labels = in.PrivateEndpoint.Labels
		case "dns_options":
			p.DnsOptions = in.PrivateEndpoint.DnsOptions
		}
	}
}
