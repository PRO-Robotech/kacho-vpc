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

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/macutil"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// niReferrerType — ReferrerType в address_references для адресов, привязанных к NIC.
const niReferrerType = "network_interface"

// niUsedByReferrerType — тип референта в NIC.used_by, когда NIC приаттачен к
// compute-инстансу (зеркало Address.used_by referrer type для NIC/NAT-адресов).
const niUsedByReferrerType = "compute_instance"

// niMacRetryAttempts — количество попыток сгенерировать уникальный MAC при
// cloud-wide UNIQUE-collision (~1e-3 на 1M NIC при 40 битах энтропии — см.
// `internal/apps/kacho/shared/macutil`).
const niMacRetryAttempts = 3

// CreateInput — параметры для CreateNetworkInterfaceUseCase.Execute. Использует
// `domain.NetworkInterface` как «несущий» носитель данных (skill evgeniy §2 B.3 —
// не плодить параллельные XxxReq, дублирующие domain). Поле `n.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixSubnet)` (NIC
// переиспользует Subnet-prefix `e9b`).
type CreateInput struct {
	NetworkInterface domain.NetworkInterface
	// InstanceID — опц. сразу приаттачить NIC к инстансу после создания.
	InstanceID string
	// Index — информационный (на какой слот инстанса вешать NIC); не персистим.
	Index string
}

// CreateNetworkInterfaceUseCase инициирует создание NIC. Sync-проверки (folder
// exists, name unique, cardinality v4/v6, address-refs) выполняются ДО создания
// Operation. Async-часть (`doCreate`) — атомарный backstop через FK / DB CHECK
// (миграция 0018, KAC-55) / UNIQUE MAC.
type CreateNetworkInterfaceUseCase struct {
	repo         NetworkInterfaceRepo
	subnetRead   SubnetReader
	addressRepo  AddressRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewCreateNetworkInterfaceUseCase создаёт CreateNetworkInterfaceUseCase.
func NewCreateNetworkInterfaceUseCase(repo NetworkInterfaceRepo, subnetRead SubnetReader, addressRepo AddressRepo, folderClient FolderClient, opsRepo operations.Repo) *CreateNetworkInterfaceUseCase {
	return &CreateNetworkInterfaceUseCase{
		repo:         repo,
		subnetRead:   subnetRead,
		addressRepo:  addressRepo,
		folderClient: folderClient,
		opsRepo:      opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
func (u *CreateNetworkInterfaceUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	n := in.NetworkInterface
	if n.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if n.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	// Domain-self-validation (skill evgeniy §4 D.5 / AP-1): Name/Description/Labels
	// + MAC через newtype Validate(). Service-слой больше НЕ зовёт corevalidate.*.
	if err := n.Validate(); err != nil {
		return nil, err
	}
	if err := validateNICAddressCardinality(n.V4AddressIDs, n.V6AddressIDs); err != nil {
		return nil, err
	}
	if err := checkFolderExists(ctx, u.folderClient, n.FolderID); err != nil {
		return nil, err
	}

	niID := ids.NewID(ids.PrefixSubnet)
	op, err := operations.New(
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

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный backstop:
// folder-exists + Subnet.Get + Address-refs валидация + маркировка (used + referrer)
// + Insert NIC. MAC-allocation retry на cloud-wide UNIQUE-collision.
func (u *CreateNetworkInterfaceUseCase) doCreate(ctx context.Context, niID string, in CreateInput) (*anypb.Any, error) {
	n := in.NetworkInterface
	exists, err := u.folderClient.Exists(ctx, n.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", n.FolderID)
	}
	if _, err := u.subnetRead.Get(ctx, n.SubnetID); err != nil {
		return nil, mapRepoErr(err)
	}
	// Валидируем ссылки на Address-ресурсы (существуют, нужной версии, в той же
	// подсети, не заняты другим референтом) и помечаем их used=true + referrer.
	// Best-effort v1: валидация/маркировка адресов и Insert NIC не в одной tx.
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
		FolderID:         n.FolderID,
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
	// MAC аллоцируется здесь и больше не меняется на жизни NIC (AWS-ENI semantics).
	// При cloud-wide UNIQUE-collision генерируем новый MAC и повторяем Insert.
	var created *domain.NetworkInterfaceRecord
	var insertErr error
	for attempt := 0; attempt < niMacRetryAttempts; attempt++ {
		mac, merr := macutil.GenerateMAC()
		if merr != nil {
			u.detachAddresses(ctx, append(append([]string{}, n.V4AddressIDs...), n.V6AddressIDs...))
			return nil, status.Errorf(codes.Internal, "generate mac: %v", merr)
		}
		rec.MAC = mac
		created, insertErr = u.repo.Insert(ctx, rec)
		if insertErr == nil {
			return marshalNetworkInterfaceRecord(created)
		}
		if !errors.Is(insertErr, ports.ErrMacCollision) {
			break
		}
	}
	// rollback маркировки адресов best-effort.
	u.detachAddresses(ctx, append(append([]string{}, n.V4AddressIDs...), n.V6AddressIDs...))
	if errors.Is(insertErr, ports.ErrMacCollision) {
		return nil, status.Errorf(codes.Internal, "could not allocate unique MAC after %d attempts", niMacRetryAttempts)
	}
	return nil, mapRepoErr(insertErr)
}

// validateAddressRef проверяет, что Address id существует, имеет ожидаемую
// IP-версию, (для internal) лежит в подсети nicSubnet и не занят. Возвращает
// gRPC-status при нарушении.
func (u *CreateNetworkInterfaceUseCase) validateAddressRef(ctx context.Context, id, nicSubnet string, want domain.IpVersion) error {
	a, err := u.addressRepo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return status.Errorf(codes.InvalidArgument, "address %s not found", id)
		}
		return mapRepoErr(err)
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

// validateAndAttachAddresses валидирует все v4/v6 address-refs, затем помечает
// каждый used=true + referrer={network_interface, nicID, nicName}. Best-effort:
// если что-то падает в середине, ранее размеченные адреса откатываются.
func (u *CreateNetworkInterfaceUseCase) validateAndAttachAddresses(ctx context.Context, nicID, nicName, nicSubnet string, v4IDs, v6IDs []string) error {
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

// detachAddresses снимает used + referrer-row с каждого address id (best-effort,
// ошибки логируются неявно — пропускаются).
func (u *CreateNetworkInterfaceUseCase) detachAddresses(ctx context.Context, ids []string) {
	for _, id := range ids {
		_ = u.addressRepo.ClearReference(ctx, id)
	}
}
