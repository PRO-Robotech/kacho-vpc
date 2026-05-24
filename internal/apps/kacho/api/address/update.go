package address

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

// UpdateInput — параметры для UpdateAddressUseCase.Execute. Address — особый
// случай: name optional (empty allowed, как и в Create). UpdateMask discipline
// — verbatim YC.
//
// Skill evgeniy §7 I.1 — XxxReq плох, если зеркалит domain; в нашем случае
// мы храним domain-поля плюс orthogonal mask + два mutable-bool'а.
type UpdateInput struct {
	AddressID          string
	Name               string
	Description        string
	Labels             map[string]string
	DeletionProtection bool
	Reserved           bool
	UpdateMask         []string
}

// UpdateAddressUseCase — sync-валидация update_mask + значений, затем создание
// Operation + async update в worker'е. Spec-поля (external/internal v4/v6) —
// hard-immutable, через mask их менять нельзя.
//
// A.7 sub-PR 2 (KAC-94): writer-TX явный, DML + outbox atomic (Address.UPDATED).
type UpdateAddressUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateAddressUseCase создаёт UpdateAddressUseCase.
func NewUpdateAddressUseCase(r Repo, opsRepo operations.Repo) *UpdateAddressUseCase {
	return &UpdateAddressUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateAddressUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, in.AddressID); err != nil {
		return nil, err
	}
	if in.AddressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if err := validateAddressUpdate(in); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update address %s", in.AddressID),
		&vpcv1.UpdateAddressMetadata{AddressId: in.AddressID},
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

func (u *UpdateAddressUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	// Get + Update внутри одной writer-TX: race-free read-modify-write.
	rec, err := w.Addresses().Get(ctx, in.AddressID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applyAddressMask(&rec.Address, in)

	updated, err := w.Addresses().Update(ctx, &rec.Address)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Address", updated.ID, "UPDATED", addressPayloadMap(updated)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	return marshalAddressRecord(updated)
}

// validateAddressUpdate проверяет name/description/labels в Update Address.
//
// В отличие от Network/Cloud/Folder/Subnet, name для Address optional —
// `name=""` валиден, regex применяется только если непустой. Wave 2 batch A
// (KAC-94): валидация через domain newtypes; corevalidate.* удалены (skill
// evgeniy §4 D.5 / AP-1).
func validateAddressUpdate(in UpdateInput) error {
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {},
		"deletion_protection": {}, "reserved": {},
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
			// VPC Address: empty name allowed (YC permissive policy).
			if err := domain.RcNameVPC(in.Name).Validate(); err != nil {
				return err
			}
		case "description":
			if err := domain.RcDescription(in.Description).Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(domain.LabelsFromMap(in.Labels)); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyAddressMask — применяет subset полей к существующему domain.Address.
// no-mask = full PATCH (verbatim YC).
func applyAddressMask(a *domain.Address, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		a.Name = domain.RcNameVPC(in.Name)
		a.Description = domain.RcDescription(in.Description)
		a.Labels = domain.LabelsFromMap(in.Labels)
		a.DeletionProtection = in.DeletionProtection
		a.Reserved = in.Reserved
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			a.Name = domain.RcNameVPC(in.Name)
		case "description":
			a.Description = domain.RcDescription(in.Description)
		case "labels":
			a.Labels = domain.LabelsFromMap(in.Labels)
		case "deletion_protection":
			a.DeletionProtection = in.DeletionProtection
		case "reserved":
			a.Reserved = in.Reserved
		}
	}
}
