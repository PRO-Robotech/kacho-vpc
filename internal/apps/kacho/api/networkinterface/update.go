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

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// UpdateInput — параметры для UpdateNetworkInterfaceUseCase.Execute. Использует
// `domain.NetworkInterface` как «несущий» носитель данных (skill evgeniy §2 B.3 —
// не плодить параллельные XxxReq, дублирующие domain). Только потенциально
// меняемые поля имеют значение (Name/Description/Labels/SecurityGroupIDs/
// V4AddressIDs/V6AddressIDs); project_id/subnet_id/mac — immutable.
type UpdateInput struct {
	NetworkInterfaceID string
	NetworkInterface   domain.NetworkInterface
	UpdateMask         []string
}

// UpdateNetworkInterfaceUseCase инициирует обновление NIC. Sync-валидация
// update_mask + значений (Name/Description/Labels). Async — diff address-refs +
// applyMask + writer.UpdateMeta + outbox-emit в одной writer-TX (skill evgeniy
// §6 G.5).
type UpdateNetworkInterfaceUseCase struct {
	repo        Repo
	addressRepo AddressRepo
	opsRepo     operations.Repo
}

// NewUpdateNetworkInterfaceUseCase создаёт UpdateNetworkInterfaceUseCase.
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
	// Domain self-validation: name/description/labels через newtype.Validate() (на
	// поля, которые клиент мог прислать; mask-aware применение делается в worker'е).
	if err := in.NetworkInterface.Validate(); err != nil {
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

// doUpdate — worker-loop Update. Открывает писатель-TX, применяет mask, делает
// UpdateMeta + outbox-emit атомарно. Address-attach diff — legacy SetReference
// best-effort (до перехода Address writer-iface на единую TX).
func (u *UpdateNetworkInterfaceUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	// Reader-TX для precondition Get (writer открываем только когда есть
	// что писать) — но проще сразу открыть writer (G.2 — writer видит свои
	// writes; Get идёт через writer).
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	rec, err := w.NetworkInterfaces().Get(ctx, in.NetworkInterfaceID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	// Если изменились address-refs — пересчитываем diff: detach убранные,
	// attach добавленные (с валидацией). Best-effort v1 (address-attach не в
	// той же writer-TX; см. doc-комментарий на iface.go).
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
		u.detachAddresses(ctx, removed)
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
		if err := u.validateAndAttachAddresses(ctx, rec.ID, derefName(in, rec), rec.SubnetID, addedV4, addedV6); err != nil {
			return nil, err
		}
	}
	nic := &rec.NetworkInterface
	applyNICMask(nic, in)
	updated, err := w.NetworkInterfaces().UpdateMeta(ctx, nic)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "NetworkInterface", updated.ID, "UPDATED", networkInterfacePayloadMap(updated)); oerr != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, mapRepoErr(cerr)
	}
	return marshalNetworkInterfaceRecord(updated)
}

// validateAndAttachAddresses / validateAddressRef / detachAddresses — повторно
// используем шаблон из Create (дублируем тут, потому что use-case'ы независимы;
// общий refactor в repo/service-leaf — TODO Wave 4).
func (u *UpdateNetworkInterfaceUseCase) validateAndAttachAddresses(ctx context.Context, nicID, nicName, nicSubnet string, v4IDs, v6IDs []string) error {
	for _, id := range v4IDs {
		if err := u.validateAddressRef(ctx, id, nicSubnet, domain.IpVersionIPv4); err != nil {
			return err
		}
	}
	for _, id := range v6IDs {
		if err := u.validateAddressRef(ctx, id, nicSubnet, domain.IpVersionIPv6); err != nil {
			return err
		}
	}
	var attached []string
	for _, id := range append(append([]string{}, v4IDs...), v6IDs...) {
		ref := &domain.AddressReference{AddressID: id, ReferrerType: niReferrerType, ReferrerID: nicID, ReferrerName: nicName}
		if _, err := u.addressRepo.SetReference(ctx, ref); err != nil {
			u.detachAddresses(ctx, attached)
			return mapRepoErr(err)
		}
		attached = append(attached, id)
	}
	return nil
}

func (u *UpdateNetworkInterfaceUseCase) validateAddressRef(ctx context.Context, id, nicSubnet string, want domain.IpVersion) error {
	// Делегируем create-time проверку: семантика идентичная (Address существует,
	// нужной IP-версии, в подсети, не занят).
	return (&CreateNetworkInterfaceUseCase{addressRepo: u.addressRepo}).validateAddressRef(ctx, id, nicSubnet, want)
}

func (u *UpdateNetworkInterfaceUseCase) detachAddresses(ctx context.Context, ids []string) {
	for _, id := range ids {
		_ = u.addressRepo.ClearReference(ctx, id)
	}
}

// derefName — name to apply: либо из mask (если включён), либо текущее имя.
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
