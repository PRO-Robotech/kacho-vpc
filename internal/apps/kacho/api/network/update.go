// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

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

// UpdateInput — параметры для UpdateNetworkUseCase.Execute. Update требует и
// domain.Network (с заявленными полями), и UpdateMask, поэтому отдельный input-тип
// оправдан: он хранит domain плюс ортогональную ему маску.
type UpdateInput struct {
	NetworkID  string
	Network    domain.Network // несет Name/Description/Labels, остальные поля не используются
	UpdateMask []string
}

// UpdateNetworkUseCase — sync-валидация update_mask + значений, затем создание
// Operation + async update в worker'е. Writer-TX явный, DML + outbox атомарны.
type UpdateNetworkUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateNetworkUseCase создает UpdateNetworkUseCase.
func NewUpdateNetworkUseCase(r Repo, opsRepo operations.Repo) *UpdateNetworkUseCase {
	return &UpdateNetworkUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateNetworkUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, in.NetworkID); err != nil {
		return nil, err
	}
	if in.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if err := serviceerr.FromValidation(validateNetworkUpdate(in)); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update network %s", in.NetworkID),
		&vpcv1.UpdateNetworkMetadata{NetworkId: in.NetworkID},
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

func (u *UpdateNetworkUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
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
	rec, err := w.Networks().GetForUpdate(ctx, in.NetworkID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	applyNetworkMask(&rec.Network, in)
	updated, err := w.Networks().Update(ctx, &rec.Network)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", updated.ID, "UPDATED", helpers.DomainToMap(updated)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	// Если labels попали в update_mask (или это full-object PATCH), переэмитим
	// register-intent с обновленными метками в ТОЙ ЖЕ writer-TX, чтобы kacho-iam
	// держал resource_mirror в актуальном виде для ARM_LABELS-селектора
	// (reconcile / revoke при смене меток). Update без labels → переэмита нет.
	// Полное удаление labels → upsert с пустыми метками (НЕ Unregister: сеть все
	// еще существует, mirror-row остается, просто перестает матчиться селектором).
	if labelsInMask(in.UpdateMask) {
		if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
			fgaregister.ProjectHierarchyItem(string(updated.ProjectID), "vpc_network", updated.ID,
				domain.LabelsToMap(updated.Labels)),
		)); err != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return marshalNetworkRecord(updated)
}

// validateNetworkUpdate — sync-проверка update_mask и значений: заявленные поля
// преобразуем в domain-newtypes и зовем их `Validate()` напрямую.
func validateNetworkUpdate(in UpdateInput) error {
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}}
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
			if err := in.Network.Name.Validate(); err != nil {
				return err
			}
		case "description":
			if err := in.Network.Description.Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(in.Network.Labels); err != nil {
				return err
			}
		}
	}
	return nil
}

// labelsInMask — затрагивает ли update_mask поле `labels`: пустая маска значит
// full-object PATCH (labels применяются), явная маска матчится, если содержит
// "labels". Управляет переэмитом register-intent — держать в синхроне с
// full-PATCH-набором полей в applyNetworkMask.
//
// Хелпер намеренно co-located с applyNetworkMask (а не вынесен в shared-пакет),
// чтобы full-PATCH-набор полей и emit-gate не разъехались; общий «shared mask
// helper» связал бы несвязанные use-case-пакеты без реальной пользы.
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

// applyNetworkMask — применяет subset полей к существующему domain.Network.
// Пустая маска = full PATCH (применяются все mutable-поля).
func applyNetworkMask(n *domain.Network, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		n.Name = in.Network.Name
		n.Description = in.Network.Description
		n.Labels = in.Network.Labels
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			n.Name = in.Network.Name
		case "description":
			n.Description = in.Network.Description
		case "labels":
			n.Labels = in.Network.Labels
		}
	}
}
