// Package service — NetworkInterface (NIC) use-cases (public CRUD/Operations +
// Attach/Detach) и internal-проекция (InternalNetworkInterfaceService).
//
// NIC — first-class сетевой интерфейс (AWS-ENI-style; epic KAC-2). Принадлежит
// подсети (и транзитивно сети/VPN). Несёт набор ссылок на Address-ресурсы
// (kacho-vpc) по id: v4_address_ids / v6_address_ids (KAC-7). Один Address —
// максимум на одном NIC: обеспечивается на уровне сервиса через addresses.used +
// referrer-tracking (Create/Update проверяют used, выставляют used=true + referrer;
// Delete/detach снимают used + referrer). Публичный NetworkInterface — lean (несёт
// только id-ссылки); резолвленные IP уходят в data-plane через internal-проекцию
// (InternalNetworkInterface.v4_addresses/v6_addresses) — workspace CLAUDE.md
// §«Инфра-чувствительные данные».
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
)

// niReferrerType — ReferrerType в address_references для адресов, привязанных к NIC.
const niReferrerType = "network_interface"

// niUsedByReferrerType — тип референта в NIC.used_by, когда NIC приаттачен к
// compute-инстансу (зеркало Address.used_by referrer type для NIC/NAT-адресов).
const niUsedByReferrerType = "compute_instance"

// NetworkInterfaceFilter — фильтр для List.
type NetworkInterfaceFilter struct {
	FolderID   string
	InstanceID string
	SubnetID   string
	// NetworkID — больше не поддерживается фильтром (NIC не хранит network_id);
	// поле оставлено для совместимости с handler'ом, репо его игнорирует.
	NetworkID string
}

// NetworkInterfaceRepo — port-интерфейс репозитория NIC.
type NetworkInterfaceRepo interface {
	Get(ctx context.Context, id string) (*domain.NetworkInterface, error)
	List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*domain.NetworkInterface, string, error)
	ListByHypervisor(ctx context.Context, hvID string) ([]*domain.NetworkInterface, error)
	Insert(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error)
	UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error)
	// SetUsedBy выставляет/очищает denorm used_by-ссылку NIC (refID="" — очистка)
	// и публичный status. Зеркало AddressRepo.SetReference/ClearReference.
	SetUsedBy(ctx context.Context, id, refType, refID, refName string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterface, error)
	SetDataplane(ctx context.Context, id string, dp domain.NICDataplane, newStatus domain.NetworkInterfaceStatus, setStatus bool) (*domain.NetworkInterface, bool, error)
	Delete(ctx context.Context, id string) error
}

const niResource = "network interface"

func niResourceID(id string) error { return corevalidate.ResourceID(niResource, ids.PrefixSubnet, id) }

// ---- public NetworkInterfaceService ----

// CreateNICReq — запрос на создание NIC.
type CreateNICReq struct {
	FolderID         string
	Name             string
	Description      string
	Labels           map[string]string
	SubnetID         string
	V4AddressIDs     []string
	V6AddressIDs     []string
	SecurityGroupIDs []string
	InstanceID       string // опц. — сразу приаттачить
	Index            string
}

// UpdateNICReq — частичное обновление NIC.
type UpdateNICReq struct {
	ID               string
	Name             string
	Description      string
	Labels           map[string]string
	SecurityGroupIDs []string
	V4AddressIDs     []string
	V6AddressIDs     []string
	UpdateMask       []string
}

// NetworkInterfaceService — бизнес-логика управления NIC.
type NetworkInterfaceService struct {
	repo         NetworkInterfaceRepo
	subnetRepo   SubnetRepo
	addressRepo  AddressRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewNetworkInterfaceService создаёт NetworkInterfaceService.
func NewNetworkInterfaceService(repo NetworkInterfaceRepo, subnetRepo SubnetRepo, addressRepo AddressRepo, folderClient FolderClient, opsRepo operations.Repo) *NetworkInterfaceService {
	return &NetworkInterfaceService{repo: repo, subnetRepo: subnetRepo, addressRepo: addressRepo, folderClient: folderClient, opsRepo: opsRepo}
}

// Get возвращает NIC по id.
func (s *NetworkInterfaceService) Get(ctx context.Context, id string) (*domain.NetworkInterface, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	n, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return n, nil
}

// List возвращает NIC фолдера (опц. фильтр по instance/subnet/network).
func (s *NetworkInterfaceService) List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*domain.NetworkInterface, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	out, next, err := s.repo.List(ctx, f, p)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	return out, next, nil
}

// Create инициирует создание NIC, возвращает Operation.
func (s *NetworkInterfaceService) Create(ctx context.Context, req CreateNICReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if err := corevalidate.NameVPC("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}
	if err := checkFolderExists(ctx, s.folderClient, req.FolderID); err != nil {
		return nil, err
	}

	niID := ids.NewID(ids.PrefixSubnet)
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Create network interface %s", req.Name), &vpcv1.CreateNetworkInterfaceMetadata{NetworkInterfaceId: niID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, niID, req)
	})
	return &op, nil
}

func (s *NetworkInterfaceService) doCreate(ctx context.Context, niID string, req CreateNICReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
	}
	if _, err := s.subnetRepo.Get(ctx, req.SubnetID); err != nil {
		return nil, mapRepoErr(err)
	}
	// Валидируем ссылки на Address-ресурсы (существуют, нужной версии, в той же
	// подсети, не заняты другим референтом) и помечаем их used=true + referrer.
	// Best-effort v1: валидация/маркировка адресов и Insert NIC не в одной tx.
	if err := s.validateAndAttachAddresses(ctx, niID, req.Name, req.SubnetID, req.V4AddressIDs, req.V6AddressIDs); err != nil {
		return nil, err
	}
	st := domain.NIStatusAvailable
	usedByType, usedByID := "", ""
	if req.InstanceID != "" {
		st = domain.NIStatusActive
		usedByType, usedByID = niUsedByReferrerType, req.InstanceID
	}
	n := &domain.NetworkInterface{
		ID:               niID,
		FolderID:         req.FolderID,
		CreatedAt:        time.Now().UTC(),
		Name:             req.Name,
		Description:      req.Description,
		Labels:           req.Labels,
		SubnetID:         req.SubnetID,
		V4AddressIDs:     req.V4AddressIDs,
		V6AddressIDs:     req.V6AddressIDs,
		SecurityGroupIDs: req.SecurityGroupIDs,
		UsedByType:       usedByType,
		UsedByID:         usedByID,
		Status:           st,
	}
	created, err := s.repo.Insert(ctx, n)
	if err != nil {
		// rollback маркировки адресов best-effort.
		s.detachAddresses(ctx, append(append([]string{}, req.V4AddressIDs...), req.V6AddressIDs...))
		return nil, mapRepoErr(err)
	}
	return anypb.New(protoconv.NetworkInterface(created))
}

// validateAddressRef проверяет, что Address id существует, имеет ожидаемую
// IP-версию, (для internal-ipv4) лежит в подсети nicSubnet и не занят. Возвращает
// gRPC-status при нарушении.
func (s *NetworkInterfaceService) validateAddressRef(ctx context.Context, id, nicSubnet string, want domain.IpVersion) error {
	a, err := s.addressRepo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
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
func (s *NetworkInterfaceService) validateAndAttachAddresses(ctx context.Context, nicID, nicName, nicSubnet string, v4IDs, v6IDs []string) error {
	for _, id := range v4IDs {
		if err := s.validateAddressRef(ctx, id, nicSubnet, domain.IpVersionIPv4); err != nil {
			return err
		}
	}
	for _, id := range v6IDs {
		if err := s.validateAddressRef(ctx, id, nicSubnet, domain.IpVersionIPv6); err != nil {
			return err
		}
	}
	var attached []string
	for _, id := range append(append([]string{}, v4IDs...), v6IDs...) {
		ref := &domain.AddressReference{AddressID: id, ReferrerType: niReferrerType, ReferrerID: nicID, ReferrerName: nicName}
		if _, err := s.addressRepo.SetReference(ctx, ref); err != nil {
			s.detachAddresses(ctx, attached)
			return mapRepoErr(err)
		}
		attached = append(attached, id)
	}
	return nil
}

// detachAddresses снимает used + referrer-row с каждого address id (best-effort,
// ошибки логируются неявно — пропускаются).
func (s *NetworkInterfaceService) detachAddresses(ctx context.Context, ids []string) {
	for _, id := range ids {
		_ = s.addressRepo.ClearReference(ctx, id)
	}
}

// Update обновляет name/description/labels/security_group_ids/v4_address_ids/v6_address_ids.
func (s *NetworkInterfaceService) Update(ctx context.Context, req UpdateNICReq) (*operations.Operation, error) {
	if err := niResourceID(req.ID); err != nil {
		return nil, err
	}
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}, "security_group_ids": {}, "v4_address_ids": {}, "v6_address_ids": {}}
	if err := corevalidate.UpdateMask("update_mask", req.UpdateMask, known); err != nil {
		return nil, err
	}
	if err := corevalidate.NameVPC("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Update network interface %s", req.ID), &vpcv1.UpdateNetworkInterfaceMetadata{NetworkInterfaceId: req.ID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		n, err := s.repo.Get(ctx, req.ID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		// Если изменились address-refs — пересчитываем diff: detach убранные,
		// attach добавленные (с валидацией). Best-effort v1 (без единой tx).
		newV4 := nicMaskV4(n, req)
		newV6 := nicMaskV6(n, req)
		if !strSetEqual(n.V4AddressIDs, newV4) || !strSetEqual(n.V6AddressIDs, newV6) {
			oldAll := append(append([]string{}, n.V4AddressIDs...), n.V6AddressIDs...)
			newAll := strSet(append(append([]string{}, newV4...), newV6...))
			// detach убранные
			var removed []string
			for _, id := range oldAll {
				if !newAll[id] {
					removed = append(removed, id)
				}
			}
			s.detachAddresses(ctx, removed)
			// validate+attach добавленные
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
			if err := s.validateAndAttachAddresses(ctx, n.ID, derefName(req, n), n.SubnetID, addedV4, addedV6); err != nil {
				return nil, err
			}
		}
		applyNICMask(n, req)
		updated, err := s.repo.UpdateMeta(ctx, n)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.NetworkInterface(updated))
	})
	return &op, nil
}

func derefName(req UpdateNICReq, n *domain.NetworkInterface) string {
	if len(req.UpdateMask) == 0 {
		return req.Name
	}
	for _, f := range req.UpdateMask {
		if f == "name" {
			return req.Name
		}
	}
	return n.Name
}

func nicMaskV4(n *domain.NetworkInterface, req UpdateNICReq) []string {
	if len(req.UpdateMask) == 0 {
		return req.V4AddressIDs
	}
	for _, f := range req.UpdateMask {
		if f == "v4_address_ids" {
			return req.V4AddressIDs
		}
	}
	return n.V4AddressIDs
}

func nicMaskV6(n *domain.NetworkInterface, req UpdateNICReq) []string {
	if len(req.UpdateMask) == 0 {
		return req.V6AddressIDs
	}
	for _, f := range req.UpdateMask {
		if f == "v6_address_ids" {
			return req.V6AddressIDs
		}
	}
	return n.V6AddressIDs
}

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

func applyNICMask(n *domain.NetworkInterface, req UpdateNICReq) {
	if len(req.UpdateMask) == 0 {
		n.Name, n.Description, n.Labels, n.SecurityGroupIDs = req.Name, req.Description, req.Labels, req.SecurityGroupIDs
		n.V4AddressIDs, n.V6AddressIDs = req.V4AddressIDs, req.V6AddressIDs
		return
	}
	for _, f := range req.UpdateMask {
		switch f {
		case "name":
			n.Name = req.Name
		case "description":
			n.Description = req.Description
		case "labels":
			n.Labels = req.Labels
		case "security_group_ids":
			n.SecurityGroupIDs = req.SecurityGroupIDs
		case "v4_address_ids":
			n.V4AddressIDs = req.V4AddressIDs
		case "v6_address_ids":
			n.V6AddressIDs = req.V6AddressIDs
		}
	}
}

// Delete удаляет NIC (FailedPrecondition если ещё приаттачен к инстансу).
func (s *NetworkInterfaceService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Delete network interface %s", id), &vpcv1.DeleteNetworkInterfaceMetadata{NetworkInterfaceId: id})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		cur, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		if cur.UsedByID != "" {
			return nil, status.Errorf(codes.FailedPrecondition, "network interface %s is still attached to %s %s; detach it first", id, cur.UsedByType, cur.UsedByID)
		}
		// Снимаем used + referrer со всех привязанных Address-ресурсов (адреса
		// не удаляем — они остаются доступными, просто свободными).
		s.detachAddresses(ctx, append(append([]string{}, cur.V4AddressIDs...), cur.V6AddressIDs...))
		if err := s.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}

// AttachToInstance приаттачивает NIC к инстансу по index.
func (s *NetworkInterfaceService) AttachToInstance(ctx context.Context, id, instanceID, index string) (*operations.Operation, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	if instanceID == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id required")
	}
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Attach network interface %s to instance %s", id, instanceID),
		&vpcv1.AttachNetworkInterfaceMetadata{NetworkInterfaceId: id, InstanceId: instanceID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		cur, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		if cur.UsedByID != "" && cur.UsedByID != instanceID {
			return nil, status.Errorf(codes.FailedPrecondition, "network interface %s is already attached to %s %s", id, cur.UsedByType, cur.UsedByID)
		}
		// index — информационный (на какой слот инстанса вешать NIC); не персистим.
		_ = index
		updated, err := s.repo.SetUsedBy(ctx, id, niUsedByReferrerType, instanceID, "", domain.NIStatusActive)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.NetworkInterface(updated))
	})
	return &op, nil
}

// DetachFromInstance отвязывает NIC от инстанса.
func (s *NetworkInterfaceService) DetachFromInstance(ctx context.Context, id string) (*operations.Operation, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Detach network interface %s", id), &vpcv1.DetachNetworkInterfaceMetadata{NetworkInterfaceId: id})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		updated, err := s.repo.SetUsedBy(ctx, id, "", "", "", domain.NIStatusAvailable)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.NetworkInterface(updated))
	})
	return &op, nil
}

// ListOperations возвращает операции конкретного NIC.
func (s *NetworkInterfaceService) ListOperations(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if err := niResourceID(id); err != nil {
		return nil, "", err
	}
	if _, err := s.repo.Get(ctx, id); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{ResourceID: id, PageSize: p.PageSize, PageToken: p.PageToken})
}

// ---- internal NetworkInterfaceInternal (InternalNetworkInterfaceService) ----

// NetworkInterfaceInternal — internal-only операции над NIC: полная проекция +
// write-back data-plane-проекции от kacho-vpc-implement.
type NetworkInterfaceInternal struct {
	repo NetworkInterfaceRepo
}

// NewNetworkInterfaceInternal создаёт NetworkInterfaceInternal.
func NewNetworkInterfaceInternal(repo NetworkInterfaceRepo) *NetworkInterfaceInternal {
	return &NetworkInterfaceInternal{repo: repo}
}

// Get возвращает NIC (с data-plane-полями).
func (s *NetworkInterfaceInternal) Get(ctx context.Context, id string) (*domain.NetworkInterface, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	n, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return n, nil
}

// ListByHypervisor возвращает все NIC на указанном HV.
func (s *NetworkInterfaceInternal) ListByHypervisor(ctx context.Context, hvID string) ([]*domain.NetworkInterface, error) {
	out, err := s.repo.ListByHypervisor(ctx, hvID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return out, nil
}

// ReportNiDataplane — write-back от kacho-vpc-implement. status: PROGRAMMING/ACTIVE/
// FAILED/DELETED (NiDataplaneStatus). ACTIVE→public ACTIVE; FAILED→public FAILED;
// DELETED→удаляем NIC; PROGRAMMING→public не трогаем. Идемпотентно по revision.
// Возвращает applied=false если revision устарела.
func (s *NetworkInterfaceInternal) ReportNiDataplane(ctx context.Context, id string, dp domain.NICDataplane, dpStatus int) (bool, error) {
	if id == "" {
		return false, status.Error(codes.InvalidArgument, "network_interface_id required")
	}
	// dpStatus: 0=UNSPEC,1=PROGRAMMING,2=ACTIVE,3=FAILED,4=DELETED (vpcv1.NiDataplaneStatus)
	if dpStatus == 4 { // DELETED — финализируем удаление NIC
		cur, err := s.repo.Get(ctx, id)
		if err != nil {
			return false, mapRepoErr(err)
		}
		if dp.Revision < cur.Dataplane.Revision {
			return false, nil
		}
		if err := s.repo.Delete(ctx, id); err != nil {
			return false, mapRepoErr(err)
		}
		return true, nil
	}
	var newStatus domain.NetworkInterfaceStatus
	setStatus := false
	switch dpStatus {
	case 2: // ACTIVE
		newStatus, setStatus = domain.NIStatusActive, true
	case 3: // FAILED
		newStatus, setStatus = domain.NIStatusFailed, true
	}
	_, applied, err := s.repo.SetDataplane(ctx, id, dp, newStatus, setStatus)
	if err != nil {
		return false, mapRepoErr(err)
	}
	return applied, nil
}
