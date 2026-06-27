// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// UpdateInput — параметры для UpdateAddressUseCase.Execute. Address — особый
// случай: name optional (пустой допустим, как и в Create).
//
// Это не зеркало domain-структуры: храним domain-поля плюс orthogonal mask и два
// mutable-bool'а.
type UpdateInput struct {
	AddressID          string
	Name               string
	Description        string
	Labels             map[string]string
	DeletionProtection bool
	Reserved           bool
	UpdateMask         []string
}

// UpdateAddressUseCase — sync-валидация update_mask и значений, затем создание
// Operation + async update в worker'е. Spec-поля (external/internal v4/v6) —
// hard-immutable, через mask их менять нельзя. Writer-TX явный: DML + outbox
// (Address.UPDATED) атомарны.
type UpdateAddressUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateAddressUseCase создает UpdateAddressUseCase.
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
	if err := serviceerr.FromValidation(validateAddressUpdate(in)); err != nil {
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
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// Get + Update внутри одной writer-TX: race-free read-modify-write.
	rec, err := w.Addresses().Get(ctx, in.AddressID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	applyAddressMask(&rec.Address, in)

	updated, err := w.Addresses().Update(ctx, &rec.Address)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Address", updated.ID, "UPDATED", helpers.DomainToMap(updated)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	// Если labels попали в update_mask (или это full-object PATCH), переэмитим
	// register-intent с обновленными метками в ТОЙ ЖЕ writer-TX, чтобы kacho-iam
	// держал resource_mirror в актуальном виде для ARM_LABELS-селектора (revoke
	// при снятии метки). Update без labels → переэмита нет. Полное снятие labels →
	// upsert с пустыми метками (НЕ Unregister: Address все еще существует). Эталон —
	// network/subnet/securitygroup update.
	if labelsInMask(in.UpdateMask) {
		if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
			fgaregister.ProjectHierarchyItem(string(updated.ProjectID), "vpc_address", updated.ID,
				domain.LabelsToMap(updated.Labels)),
		)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return marshalAddressRecord(updated)
}

// labelsInMask — затрагивает ли update_mask поле `labels`: пустая маска значит
// full-object PATCH (labels применяются), явная маска матчится, если содержит
// "labels". Управляет переэмитом register-intent — держать в синхроне с
// full-PATCH-набором полей в applyAddressMask.
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

// validateAddressUpdate проверяет name/description/labels в Update Address.
//
// В отличие от других ресурсов, name для Address optional — `name=""` валиден,
// regex применяется только если непустой. Валидация — через domain newtypes.
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
			// Address: пустое имя допустимо (разрешительная политика).
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
// Пустой mask = full PATCH.
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
