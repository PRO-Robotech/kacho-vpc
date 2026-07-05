// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

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
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// UpdateInput — параметры для UpdateNetworkInterfaceUseCase.Execute. Несущим
// носителем данных служит сам `domain.NetworkInterface`, чтобы не плодить
// параллельный XxxReq, дублирующий domain. Значимы только потенциально меняемые
// поля (Name/Description/Labels/SecurityGroupIDs/V4AddressIDs/V6AddressIDs);
// project_id/subnet_id/mac — immutable.
type UpdateInput struct {
	NetworkInterfaceID string
	NetworkInterface   domain.NetworkInterface
	UpdateMask         []string
}

// UpdateNetworkInterfaceUseCase инициирует обновление NIC. Sync-часть валидирует
// update_mask и значения (Name/Description/Labels). Async-часть в одной writer-TX
// делает diff address-refs + applyMask + writer.UpdateMeta + outbox-emit.
type UpdateNetworkInterfaceUseCase struct {
	repo        Repo
	addressRepo AddressRepo
	opsRepo     operations.Repo
}

// NewUpdateNetworkInterfaceUseCase создает UpdateNetworkInterfaceUseCase.
func NewUpdateNetworkInterfaceUseCase(r Repo, addressRepo AddressRepo, opsRepo operations.Repo) *UpdateNetworkInterfaceUseCase {
	return &UpdateNetworkInterfaceUseCase{repo: r, addressRepo: addressRepo, opsRepo: opsRepo}
}

// Execute — sync-валидация и запуск Update в worker'е.
func (u *UpdateNetworkInterfaceUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := niResourceID(in.NetworkInterfaceID); err != nil {
		return nil, err
	}
	if in.NetworkInterfaceID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {},
		"security_group_ids": {}, "v4_address_ids": {}, "v6_address_ids": {},
	}
	if err := corevalidate.UpdateMask("update_mask", in.UpdateMask, known); err != nil {
		return nil, err
	}
	// Domain self-validation: name/description/labels через newtype.Validate() —
	// для полей, которые клиент мог прислать; mask-aware применение — в worker'е.
	if err := serviceerr.FromValidation(in.NetworkInterface.Validate()); err != nil {
		return nil, err
	}
	if err := validateNICAddressCardinality(in.NetworkInterface.V4AddressIDs, in.NetworkInterface.V6AddressIDs); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update network interface %s", in.NetworkInterfaceID),
		&vpcv1.UpdateNetworkInterfaceMetadata{NetworkInterfaceId: in.NetworkInterfaceID},
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

// doUpdate — async-тело Update. Detach убранных + attach добавленных address-refs
// + UpdateMeta + outbox-emit — все в ОДНОЙ writer-TX, иначе при сбое UpdateMeta
// address_references остались бы рассинхронизированы с метаданными NIC. На любой
// ошибке `defer w.Abort()` откатывает весь diff — компенсация не нужна.
func (u *UpdateNetworkInterfaceUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	rec, err := w.NetworkInterfaces().Get(ctx, in.NetworkInterfaceID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	// address-refs diff — в той же writer-TX (`w.Addresses()`).
	ar := w.Addresses()
	newV4 := nicMaskV4(rec, in)
	newV6 := nicMaskV6(rec, in)
	if !strSetEqual(rec.V4AddressIDs, newV4) || !strSetEqual(rec.V6AddressIDs, newV6) {
		oldAll := append(append([]string{}, rec.V4AddressIDs...), rec.V6AddressIDs...)
		newAll := strSet(append(append([]string{}, newV4...), newV6...))
		var removed []string
		for _, id := range oldAll {
			if !newAll[id] {
				removed = append(removed, id)
			}
		}
		if derr := detachNICAddresses(ctx, ar, removed); derr != nil {
			return nil, derr
		}
		oldAllSet := strSet(oldAll)
		var addedV4, addedV6 []string
		for _, id := range newV4 {
			if !oldAllSet[id] {
				addedV4 = append(addedV4, id)
			}
		}
		for _, id := range newV6 {
			if !oldAllSet[id] {
				addedV6 = append(addedV6, id)
			}
		}
		if err := attachNICAddresses(ctx, ar, rec.ID, derefName(in, rec), rec.SubnetID, addedV4, addedV6); err != nil {
			return nil, err
		}
	}
	nic := &rec.NetworkInterface
	applyNICMask(nic, in)
	updated, err := w.NetworkInterfaces().UpdateMeta(ctx, nic)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "NetworkInterface", updated.ID, "UPDATED", helpers.DomainToMap(updated)); oerr != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	// Если labels попали в update_mask (или это full-object PATCH), переэмитим
	// register-intent с обновленными метками в ТОЙ ЖЕ writer-TX, чтобы kacho-iam
	// держал resource_mirror в актуальном виде для ARM_LABELS-селектора (revoke
	// при снятии метки). Update без labels → переэмита нет. Полное снятие labels →
	// upsert с пустыми метками (НЕ Unregister: NIC все еще существует). Эталон —
	// network/subnet/securitygroup update.
	if labelsInMask(in.UpdateMask) {
		if rerr := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
			fgaregister.ProjectHierarchyItem(string(updated.ProjectID), "vpc_network_interface", updated.ID,
				domain.LabelsToMap(updated.Labels)),
		)); rerr != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, rerr))
		}
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, serviceerr.MapRepoErr(cerr)
	}
	return marshalNetworkInterfaceRecord(updated)
}

// labelsInMask — затрагивает ли update_mask поле `labels`: пустая маска значит
// full-object PATCH (labels применяются), явная маска матчится, если содержит
// "labels". Управляет переэмитом register-intent — держать в синхроне с
// full-PATCH-набором полей в applyNICMask.
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

// derefName — name to apply: либо из mask (если включен), либо текущее имя.
func derefName(in UpdateInput, rec *kachorepo.NetworkInterfaceRecord) string {
	if len(in.UpdateMask) == 0 {
		return string(in.NetworkInterface.Name)
	}
	for _, f := range in.UpdateMask {
		if f == "name" {
			return string(in.NetworkInterface.Name)
		}
	}
	return string(rec.Name)
}

// nicMaskV4 — какой набор v4_address_ids применять (новый или текущий).
func nicMaskV4(rec *kachorepo.NetworkInterfaceRecord, in UpdateInput) []string {
	if len(in.UpdateMask) == 0 {
		return in.NetworkInterface.V4AddressIDs
	}
	for _, f := range in.UpdateMask {
		if f == "v4_address_ids" {
			return in.NetworkInterface.V4AddressIDs
		}
	}
	return rec.V4AddressIDs
}

// nicMaskV6 — какой набор v6_address_ids применять.
func nicMaskV6(rec *kachorepo.NetworkInterfaceRecord, in UpdateInput) []string {
	if len(in.UpdateMask) == 0 {
		return in.NetworkInterface.V6AddressIDs
	}
	for _, f := range in.UpdateMask {
		if f == "v6_address_ids" {
			return in.NetworkInterface.V6AddressIDs
		}
	}
	return rec.V6AddressIDs
}

// strSet / strSetEqual — мини-helper'ы для diff-логики address-refs.
func strSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func strSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := strSet(a), strSet(b)
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}

// applyNICMask — применяет subset полей UpdateInput к существующему domain.NIC.
// Пустой mask = full-PATCH (применяются все mutable-поля).
func applyNICMask(n *domain.NetworkInterface, in UpdateInput) {
	src := in.NetworkInterface
	if len(in.UpdateMask) == 0 {
		n.Name = src.Name
		n.Description = src.Description
		n.Labels = src.Labels
		n.SecurityGroupIDs = src.SecurityGroupIDs
		n.V4AddressIDs, n.V6AddressIDs = src.V4AddressIDs, src.V6AddressIDs
		return
	}
	for _, f := range in.UpdateMask {
		switch f {
		case "name":
			n.Name = src.Name
		case "description":
			n.Description = src.Description
		case "labels":
			n.Labels = src.Labels
		case "security_group_ids":
			n.SecurityGroupIDs = src.SecurityGroupIDs
		case "v4_address_ids":
			n.V4AddressIDs = src.V4AddressIDs
		case "v6_address_ids":
			n.V6AddressIDs = src.V6AddressIDs
		}
	}
}
