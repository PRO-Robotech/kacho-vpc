// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/macutil"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// niReferrerType — ReferrerType в address_references для адресов, привязанных к NIC.
const niReferrerType = "network_interface"

// niUsedByReferrerType — тип референта в NIC.used_by, когда NIC приаттачен к
// compute-инстансу (зеркало referrer type у Address.used_by).
const niUsedByReferrerType = "compute_instance"

// niMacRetryAttempts — число попыток сгенерировать уникальный MAC при
// cloud-wide UNIQUE-collision (~1e-3 на 1M NIC при 40 битах энтропии — см.
// `internal/apps/kacho/shared/macutil`).
const niMacRetryAttempts = 3

// CreateInput — параметры для CreateNetworkInterfaceUseCase.Execute.
//
// Композиция `domain.NetworkInterface` + request-only поля (`InstanceID` /
// `Index` для immediate-attach к Compute.Instance): InstanceID/Index — атрибуты
// запроса (immediate-attach mode), а не самого ресурса, поэтому это не тривиальная
// обертка `{NetworkInterface: …}`.
//
// Поле `n.ID` на входе пустое — назначаем внутри use-case'а через
// `ids.NewID(ids.PrefixNetworkInterface)` (NIC имеет собственный prefix `nic`).
type CreateInput struct {
	NetworkInterface domain.NetworkInterface
	// InstanceID — опц. сразу приаттачить NIC к инстансу после создания.
	InstanceID string
	// Index — информационный (на какой слот инстанса вешать NIC); не персистим.
	Index string
}

// CreateNetworkInterfaceUseCase инициирует создание NIC. Sync-проверки (name
// валиден, cardinality v4/v6, address-refs) выполняются ДО создания Operation.
// Async-часть (`doCreate`) опирается на атомарный DB-backstop: FK / CHECK /
// UNIQUE MAC.
//
// Worker открывает ОДНУ writer-TX и делает в ней Insert(NIC) + outbox-emit
// атомарно. Address-attach (SetReference на addresses) пока идет через AddressRepo
// отдельной TX — Address еще не полностью переведен на CQRS-writer для SetReference.
//
// Parent-Subnet validation в `doCreate` идет через `kachoRepo.Reader().Subnets().Get`
// (Reader-TX, уходит на slave-pool, если он настроен).
type CreateNetworkInterfaceUseCase struct {
	repo          Repo
	addressRepo   AddressRepo
	projectClient ProjectClient
	opsRepo       operations.Repo
}

// NewCreateNetworkInterfaceUseCase создает CreateNetworkInterfaceUseCase.
func NewCreateNetworkInterfaceUseCase(r Repo, addressRepo AddressRepo, projectClient ProjectClient, opsRepo operations.Repo) *CreateNetworkInterfaceUseCase {
	return &CreateNetworkInterfaceUseCase{
		repo:          r,
		addressRepo:   addressRepo,
		projectClient: projectClient,
		opsRepo:       opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
func (u *CreateNetworkInterfaceUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	n := in.NetworkInterface
	if n.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if n.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	// Domain-self-validation: Name/Description/Labels + MAC + cardinality v4/v6
	// через newtype Validate(). Service-слой не зовет corevalidate.* для этих
	// инвариантов.
	if err := serviceerr.FromValidation(n.Validate()); err != nil {
		return nil, err
	}
	// validateNICAddressCardinality — fast-fail с понятным `invalidArg` (и
	// BadRequest-details); domain.Validate тоже это проверяет, но дает generic
	// error. См. helpers.go.
	if err := validateNICAddressCardinality(n.V4AddressIDs, n.V6AddressIDs); err != nil {
		return nil, err
	}
	// Sync project.Exists precheck здесь не делаем: он race-prone — между sync-
	// проверкой и async-частью project может удалить peer-сервис. NotFound для
	// несуществующего project'а возвращается через `operation.error` из `doCreate`.

	niID := ids.NewID(ids.PrefixNetworkInterface)
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create network interface %s", string(n.Name)),
		&vpcv1.CreateNetworkInterfaceMetadata{NetworkInterfaceId: niID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, niID, in)
	})
	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а): project-exists +
// Subnet.Get + валидация Address-refs + маркировка (used + referrer) + Insert NIC,
// с retry MAC-allocation на cloud-wide UNIQUE-collision.
//
// Insert(NIC) + outbox-emit идут в одной writer-TX. Address-маркировка — отдельная
// TX в `AddressRepo.SetReference`; rollback маркировки при ошибке Insert делается
// best-effort через `detachAddresses`.
func (u *CreateNetworkInterfaceUseCase) doCreate(ctx context.Context, niID string, in CreateInput) (*anypb.Any, error) {
	n := in.NetworkInterface
	exists, err := u.projectClient.Exists(ctx, n.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "project check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Project %s not found", n.ProjectID)
	}
	// Parent-Subnet check через CQRS-Reader (на slave-pool, если он настроен).
	// DB-backstop остается: FK `network_interfaces.subnet_id → subnets.id` ON
	// DELETE RESTRICT — если между Get и Insert'ом подсеть удалят, Insert упадет
	// с foreign_key_violation → `mapRepoErr` → FailedPrecondition.
	rd, rerr := u.repo.Reader(ctx)
	if rerr != nil {
		return nil, serviceerr.MapRepoErr(rerr)
	}
	_, serr := rd.Subnets().Get(ctx, n.SubnetID)
	_ = rd.Close()
	if serr != nil {
		return nil, serviceerr.MapRepoErr(serr)
	}
	// Валидируем ссылки на Address-ресурсы (существуют, нужной версии, в той же
	// подсети, не заняты другим референтом) и помечаем их used=true + referrer.
	// Best-effort v1: валидация/маркировка адресов и Insert NIC не в одной tx —
	// rollback маркировки при ошибке делается через `detachAddresses` ниже.
	if err := u.validateAndAttachAddresses(ctx, niID, string(n.Name), n.SubnetID, n.V4AddressIDs, n.V6AddressIDs); err != nil {
		return nil, err
	}
	st := domain.NIStatusAvailable
	usedByType, usedByID := "", ""
	if in.InstanceID != "" {
		st = domain.NIStatusActive
		usedByType, usedByID = niUsedByReferrerType, in.InstanceID
	}
	rec := &domain.NetworkInterface{
		ID:               niID,
		ProjectID:        n.ProjectID,
		Name:             n.Name,
		Description:      n.Description,
		Labels:           n.Labels,
		SubnetID:         n.SubnetID,
		V4AddressIDs:     n.V4AddressIDs,
		V6AddressIDs:     n.V6AddressIDs,
		SecurityGroupIDs: n.SecurityGroupIDs,
		UsedByType:       usedByType,
		UsedByID:         usedByID,
		Status:           st,
	}
	allAddrs := append(append([]string{}, n.V4AddressIDs...), n.V6AddressIDs...)
	// MAC аллоцируется здесь и больше не меняется на протяжении жизни NIC.
	// При cloud-wide UNIQUE-collision генерируем новый MAC и повторяем Insert.
	// Каждая попытка — отдельная writer-TX (CAS-конфликт на MAC требует start-over).
	for attempt := 0; attempt < niMacRetryAttempts; attempt++ {
		mac, merr := macutil.GenerateMAC()
		if merr != nil {
			u.detachAddresses(ctx, allAddrs)
			return nil, status.Errorf(codes.Internal, "generate mac: %v", merr)
		}
		rec.MAC = mac

		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			u.detachAddresses(ctx, allAddrs)
			return nil, serviceerr.MapRepoErr(werr)
		}
		created, insertErr := w.NetworkInterfaces().Insert(ctx, rec)
		if insertErr != nil {
			w.Abort()
			if errors.Is(insertErr, repo.ErrMacCollision) {
				continue // retry с новым MAC
			}
			u.detachAddresses(ctx, allAddrs)
			return nil, serviceerr.MapRepoErr(insertErr)
		}
		if oerr := w.Outbox().Emit(ctx, "NetworkInterface", created.ID, "CREATED", helpers.DomainToMap(created)); oerr != nil {
			w.Abort()
			u.detachAddresses(ctx, allAddrs)
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		// Публикуем intent на owner-hierarchy-tuple vpc_network_interface→project
		// в той же writer-TX — чтобы он не терялся при ошибке после commit. В
		// mirror-feed несем labels NIC + parent_project_id (ProjectHierarchyItem),
		// а не голый tuple — иначе resource_mirror в kacho-iam остается без labels и
		// ARM_LABELS-селектор не матчит даже свежесозданный NIC. Симметрично
		// network/subnet/securitygroup create.
		if rerr := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
			fgaregister.ProjectHierarchyItem(string(n.ProjectID), "vpc_network_interface", created.ID,
				domain.LabelsToMap(created.Labels)),
		)); rerr != nil {
			w.Abort()
			u.detachAddresses(ctx, allAddrs)
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, rerr))
		}
		if cerr := w.Commit(); cerr != nil {
			u.detachAddresses(ctx, allAddrs)
			return nil, serviceerr.MapRepoErr(cerr)
		}
		return marshalNetworkInterfaceRecord(created)
	}
	// Все попытки исчерпаны — rollback маркировки адресов best-effort.
	u.detachAddresses(ctx, allAddrs)
	return nil, status.Errorf(codes.Internal, "could not allocate unique MAC after %d attempts", niMacRetryAttempts)
}

// validateNICAddressRef проверяет, что Address id существует, имеет ожидаемую
// IP-версию, (для internal) лежит в подсети nicSubnet и не занят. Свободная
// функция поверх любого AddressRepo (в т.ч. `w.Addresses()` writer-TX) — общая
// для Create и Update. При нарушении возвращает gRPC-status.
func validateNICAddressRef(ctx context.Context, ar AddressRepo, id, nicSubnet string, want domain.IpVersion) error {
	a, err := ar.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return status.Errorf(codes.InvalidArgument, "address %s not found", id)
		}
		return serviceerr.MapRepoErr(err)
	}
	switch want {
	case domain.IpVersionIPv4:
		if a.Type != domain.AddressTypeInternal || a.InternalIpv4 == nil {
			return status.Errorf(codes.InvalidArgument, "address %s is not an internal IPv4 address", id)
		}
		if a.InternalIpv4.SubnetID != nicSubnet {
			return status.Errorf(codes.InvalidArgument, "address %s belongs to subnet %s, not %s", id, a.InternalIpv4.SubnetID, nicSubnet)
		}
	case domain.IpVersionIPv6:
		if a.IpVersion != domain.IpVersionIPv6 || a.InternalIpv6 == nil {
			return status.Errorf(codes.InvalidArgument, "address %s is not an internal IPv6 address", id)
		}
		if a.InternalIpv6.SubnetID != nicSubnet {
			return status.Errorf(codes.InvalidArgument, "address %s belongs to subnet %s, not %s", id, a.InternalIpv6.SubnetID, nicSubnet)
		}
	}
	if a.Used {
		return status.Errorf(codes.FailedPrecondition, "address %s is already in use", id)
	}
	return nil
}

// validateAndAttachAddresses (Create) — best-effort: attach идет ДО writer-TX
// NIC-insert'а (отдельные SetReference-TX), поэтому при сбое нужна явная
// компенсация detachAddresses. Делегирует общим attach/detach-функциям.
//
// Защита от attach-race: SetReference на repo-уровне делает atomic CAS на
// `addresses.used` — параллельная попытка занять тот же адрес → ErrFailedPrecondition.
// Ошибка ловится, уже-attached адреса откатываются, ошибка возвращается клиенту.
func (u *CreateNetworkInterfaceUseCase) validateAndAttachAddresses(ctx context.Context, nicID, nicName, nicSubnet string, v4IDs, v6IDs []string) error {
	if err := attachNICAddresses(ctx, u.addressRepo, nicID, nicName, nicSubnet, v4IDs, v6IDs); err != nil {
		// Компенсация: attach частично закоммитился (отдельные TX), откатываем.
		all := append(append([]string{}, v4IDs...), v6IDs...)
		_ = detachNICAddresses(ctx, u.addressRepo, all)
		return err
	}
	return nil
}

// detachAddresses (Create) — best-effort компенсация (ошибки игнорируются).
func (u *CreateNetworkInterfaceUseCase) detachAddresses(ctx context.Context, ids []string) {
	_ = detachNICAddresses(ctx, u.addressRepo, ids)
}

// attachNICAddresses — валидирует и помечает used=true + referrer для каждого
// v4/v6 address id поверх любого AddressRepo (в т.ч. `w.Addresses()` writer-TX).
// На ошибке НЕ компенсирует — это решает caller (writer-TX Abort у Update;
// detachNICAddresses у Create). Общая для Create/Update (убрана дупликация).
func attachNICAddresses(ctx context.Context, ar AddressRepo, nicID, nicName, nicSubnet string, v4IDs, v6IDs []string) error {
	for _, id := range v4IDs {
		if err := validateNICAddressRef(ctx, ar, id, nicSubnet, domain.IpVersionIPv4); err != nil {
			return err
		}
	}
	for _, id := range v6IDs {
		if err := validateNICAddressRef(ctx, ar, id, nicSubnet, domain.IpVersionIPv6); err != nil {
			return err
		}
	}
	for _, id := range append(append([]string{}, v4IDs...), v6IDs...) {
		ref := &domain.AddressReference{AddressID: id, ReferrerType: niReferrerType, ReferrerID: nicID, ReferrerName: nicName}
		if _, err := ar.SetReference(ctx, ref); err != nil {
			return serviceerr.MapRepoErr(err)
		}
	}
	return nil
}

// detachNICAddresses — снимает used + referrer-row с каждого address id поверх
// любого AddressRepo. ErrNotFound терпим (адрес мог быть удален), остальное
// возвращается. Общая для Create (best-effort, ошибки caller игнорирует) и
// Update (в writer-TX — ошибка откатывает весь diff).
func detachNICAddresses(ctx context.Context, ar AddressRepo, ids []string) error {
	for _, id := range ids {
		if err := ar.ClearReference(ctx, id); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return serviceerr.MapRepoErr(err)
		}
	}
	return nil
}
