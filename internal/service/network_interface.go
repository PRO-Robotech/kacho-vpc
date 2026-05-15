// Package service — NetworkInterface (NIC) use-cases (public CRUD/Operations +
// Attach/Detach).
//
// NIC — first-class сетевой интерфейс (AWS-ENI-style; epic KAC-2). Принадлежит
// подсети (и транзитивно сети). Несёт набор ссылок на Address-ресурсы
// (kacho-vpc) по id: v4_address_ids / v6_address_ids (KAC-7). Один Address —
// максимум на одном NIC: обеспечивается на уровне сервиса через addresses.used +
// referrer-tracking (Create/Update проверяют used, выставляют used=true + referrer;
// Delete/detach снимают used + referrer). Публичный NetworkInterface — lean (несёт
// только id-ссылки). Раньше существовала internal-проекция NetworkInterfaceInternal /
// InternalNetworkInterfaceService с data-plane-полями (hv_id/sid/sid_seq/...), но
// она удалена в KAC-79/KAC-36 (post-kube-ovn: kube-ovn управляет underlay сам, у
// kacho-vpc больше нет своих data-plane проекций).
package service

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// type2pb регистрирует DTO-трансферы в init() — нужны для dto.Transfer.
	// Skill evgeniy §3 C.4.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
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
//
// Wave 2 batch C (KAC-94): Get/List/Insert/UpdateMeta/SetUsedBy возвращают
// *domain.NetworkInterfaceRecord (repo-entity с CreatedAt). Insert/UpdateMeta
// принимают *domain.NetworkInterface (без CreatedAt — DB managed).
type NetworkInterfaceRepo interface {
	Get(ctx context.Context, id string) (*domain.NetworkInterfaceRecord, error)
	List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*domain.NetworkInterfaceRecord, string, error)
	// ListBySubnet возвращает все NIC, привязанные к указанной подсети (без
	// пагинации — используется для precondition-проверки при Subnet.Delete).
	ListBySubnet(ctx context.Context, subnetID string) ([]*domain.NetworkInterfaceRecord, error)
	Insert(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterfaceRecord, error)
	UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterfaceRecord, error)
	// SetUsedBy выставляет/очищает denorm used_by-ссылку NIC (refID="" — очистка)
	// и публичный status. Зеркало AddressRepo.SetReference/ClearReference.
	SetUsedBy(ctx context.Context, id, refType, refID, refName string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterfaceRecord, error)
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
func (s *NetworkInterfaceService) Get(ctx context.Context, id string) (*domain.NetworkInterfaceRecord, error) {
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
func (s *NetworkInterfaceService) List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*domain.NetworkInterfaceRecord, string, error) {
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
//
// Wave 2 batch C (KAC-94): валидация name/description/labels — через
// domain.NetworkInterface.Validate() (skill evgeniy §4 D.5 / AP-1). Service-слой
// больше НЕ вызывает corevalidate.NameVPC/Description/Labels. Cardinality v4/v6
// (≤1) и folder existence остаются service-level (cross-cutting / cross-resource).
func (s *NetworkInterfaceService) Create(ctx context.Context, req CreateNICReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	// Domain self-validation: name/description/labels через newtype.Validate()
	// + MAC-формат (если задан — на Create обычно пуст, аллокацию делает doCreate).
	nicValidate := domain.NetworkInterface{
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
	}
	if err := nicValidate.Validate(); err != nil {
		return nil, err
	}
	if err := validateNICAddressCardinality(req.V4AddressIDs, req.V6AddressIDs); err != nil {
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
		Name:             domain.RcNameVPC(req.Name),
		Description:      domain.RcDescription(req.Description),
		Labels:           domain.LabelsFromMap(req.Labels),
		SubnetID:         req.SubnetID,
		V4AddressIDs:     req.V4AddressIDs,
		V6AddressIDs:     req.V6AddressIDs,
		SecurityGroupIDs: req.SecurityGroupIDs,
		UsedByType:       usedByType,
		UsedByID:         usedByID,
		Status:           st,
	}
	// MAC аллоцируется здесь и больше не меняется на жизни NIC (AWS-ENI semantics).
	// При cloud-wide UNIQUE-collision (вероятность ~1e-3 на 1M NIC при 40 битах
	// энтропии — см. service/mac.go) генерируем новый MAC и повторяем Insert.
	const niMacRetryAttempts = 3
	var created *domain.NetworkInterfaceRecord
	var insertErr error
	for attempt := 0; attempt < niMacRetryAttempts; attempt++ {
		mac, err := GenerateMAC()
		if err != nil {
			s.detachAddresses(ctx, append(append([]string{}, req.V4AddressIDs...), req.V6AddressIDs...))
			return nil, status.Errorf(codes.Internal, "generate mac: %v", err)
		}
		n.MAC = mac
		created, insertErr = s.repo.Insert(ctx, n)
		if insertErr == nil {
			return marshalNetworkInterfaceRecord(created)
		}
		if !errors.Is(insertErr, ErrMacCollision) {
			break
		}
	}
	// rollback маркировки адресов best-effort.
	s.detachAddresses(ctx, append(append([]string{}, req.V4AddressIDs...), req.V6AddressIDs...))
	if errors.Is(insertErr, ErrMacCollision) {
		return nil, status.Errorf(codes.Internal, "could not allocate unique MAC after %d attempts", niMacRetryAttempts)
	}
	return nil, mapRepoErr(insertErr)
}

// marshalNetworkInterfaceRecord конвертирует repo-entity NIC в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4: protoconv.NetworkInterface → dto.Transfer).
func marshalNetworkInterfaceRecord(rec *domain.NetworkInterfaceRecord) (*anypb.Any, error) {
	var dst *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer NetworkInterface: %w", err)
	}
	return anypb.New(dst)
}

// validateAddressRef проверяет, что Address id существует, имеет ожидаемую
// IP-версию, (для internal-ipv4) лежит в подсети nicSubnet и не занят. Возвращает
// gRPC-status при нарушении.
//
// Wave 2 batch C (KAC-94): принимает *domain.AddressRecord (с CreatedAt) —
// AddressRepo.Get теперь возвращает Record.
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

// validateNICAddressCardinality fast-fail sync-валидация: на одной NetworkInterface
// разрешён максимум один IPv4 и максимум один IPv6 (KAC-55). Совпадает с DB-уровнем
// `network_interfaces_v4_addr_max1` / `_v6_addr_max1` (миграция 0018) — DB-side как
// финальный backstop, эта функция даёт понятный InvalidArgument до создания Operation.
// Multi-IP per VM реализуется через несколько NIC, не через secondary addresses в
// одном NIC (упрощённая модель vs AWS ENI; зеркалит verbatim YC compute API).
func validateNICAddressCardinality(v4IDs, v6IDs []string) error {
	if len(v4IDs) > 1 {
		return invalidArg("v4_address_ids", "at most one IPv4 address per network interface (use multiple NICs for multi-IP)")
	}
	if len(v6IDs) > 1 {
		return invalidArg("v6_address_ids", "at most one IPv6 address per network interface (use multiple NICs for multi-IP)")
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
//
// Wave 2 batch C (KAC-94): валидация name/description/labels — через
// domain.NetworkInterface.Validate() (skill evgeniy §4 D.5 / AP-1). Service-слой
// больше НЕ вызывает corevalidate.NameVPC/Description/Labels.
func (s *NetworkInterfaceService) Update(ctx context.Context, req UpdateNICReq) (*operations.Operation, error) {
	if err := niResourceID(req.ID); err != nil {
		return nil, err
	}
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}, "security_group_ids": {}, "v4_address_ids": {}, "v6_address_ids": {}}
	if err := corevalidate.UpdateMask("update_mask", req.UpdateMask, known); err != nil {
		return nil, err
	}
	// Domain self-validation на наполнение req — newtype.Validate() в lazy-init
	// форме (мы валидируем поля, которые клиент мог обновить; mask-aware применение
	// делается уже в worker'е через applyNICMask).
	nicValidate := domain.NetworkInterface{
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
	}
	if err := nicValidate.Validate(); err != nil {
		return nil, err
	}
	if err := validateNICAddressCardinality(req.V4AddressIDs, req.V6AddressIDs); err != nil {
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
		rec, err := s.repo.Get(ctx, req.ID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		// Если изменились address-refs — пересчитываем diff: detach убранные,
		// attach добавленные (с валидацией). Best-effort v1 (без единой tx).
		newV4 := nicMaskV4(rec, req)
		newV6 := nicMaskV6(rec, req)
		if !strSetEqual(rec.V4AddressIDs, newV4) || !strSetEqual(rec.V6AddressIDs, newV6) {
			oldAll := append(append([]string{}, rec.V4AddressIDs...), rec.V6AddressIDs...)
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
			if err := s.validateAndAttachAddresses(ctx, rec.ID, derefName(req, rec), rec.SubnetID, addedV4, addedV6); err != nil {
				return nil, err
			}
		}
		nic := &rec.NetworkInterface
		applyNICMask(nic, req)
		updated, err := s.repo.UpdateMeta(ctx, nic)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return marshalNetworkInterfaceRecord(updated)
	})
	return &op, nil
}

func derefName(req UpdateNICReq, rec *domain.NetworkInterfaceRecord) string {
	if len(req.UpdateMask) == 0 {
		return req.Name
	}
	for _, f := range req.UpdateMask {
		if f == "name" {
			return req.Name
		}
	}
	return string(rec.Name)
}

func nicMaskV4(rec *domain.NetworkInterfaceRecord, req UpdateNICReq) []string {
	if len(req.UpdateMask) == 0 {
		return req.V4AddressIDs
	}
	for _, f := range req.UpdateMask {
		if f == "v4_address_ids" {
			return req.V4AddressIDs
		}
	}
	return rec.V4AddressIDs
}

func nicMaskV6(rec *domain.NetworkInterfaceRecord, req UpdateNICReq) []string {
	if len(req.UpdateMask) == 0 {
		return req.V6AddressIDs
	}
	for _, f := range req.UpdateMask {
		if f == "v6_address_ids" {
			return req.V6AddressIDs
		}
	}
	return rec.V6AddressIDs
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
		n.Name = domain.RcNameVPC(req.Name)
		n.Description = domain.RcDescription(req.Description)
		n.Labels = domain.LabelsFromMap(req.Labels)
		n.SecurityGroupIDs = req.SecurityGroupIDs
		n.V4AddressIDs, n.V6AddressIDs = req.V4AddressIDs, req.V6AddressIDs
		return
	}
	for _, f := range req.UpdateMask {
		switch f {
		case "name":
			n.Name = domain.RcNameVPC(req.Name)
		case "description":
			n.Description = domain.RcDescription(req.Description)
		case "labels":
			n.Labels = domain.LabelsFromMap(req.Labels)
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
		// Software fast-path: дешёвый Get с понятным error-message для типичного
		// случая (NIC уже attached к видимому owner-у). Не race-safe сам по себе —
		// финальная защита от TOCTOU делается на DB-уровне в s.repo.SetUsedBy
		// (атомарный conditional UPDATE + partial UNIQUE network_interfaces_used_by_uniq,
		// миграция 0016 / KAC-52; workspace CLAUDE.md §«Within-service refs —
		// DB-уровень обязателен», запрет #10).
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
			// CAS-конфликт: repo вернул ErrFailedPrecondition. Race-обогащённый
			// message — догружаем actual owner для пользователя.
			if errors.Is(err, ErrFailedPrecondition) {
				if actual, gerr := s.repo.Get(ctx, id); gerr == nil && actual.UsedByID != "" {
					return nil, status.Errorf(codes.FailedPrecondition,
						"network interface %s is already attached to %s %s", id, actual.UsedByType, actual.UsedByID)
				}
				return nil, status.Errorf(codes.FailedPrecondition,
					"network interface %s attach raced; already owned by another resource", id)
			}
			return nil, mapRepoErr(err)
		}
		return marshalNetworkInterfaceRecord(updated)
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
		return marshalNetworkInterfaceRecord(updated)
	})
	return &op, nil
}

// ListOperations возвращает операции конкретного NIC.
func (s *NetworkInterfaceService) ListOperations(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if err := niResourceID(id); err != nil {
		return nil, "", err
	}
	// NB: no repo.Get precondition — operation history must remain reachable
	// after the resource is deleted (operations rows have no FK cascade).
	return s.opsRepo.List(ctx, operations.ListFilter{ResourceID: id, PageSize: p.PageSize, PageToken: p.PageToken})
}

// NetworkInterfaceInternal / InternalNetworkInterfaceService / ReportNiDataplane /
// ListByHypervisor / SetDataplane — удалены в KAC-79/KAC-36 (post-kube-ovn:
// data-plane-проекция NIC + write-back от kacho-vpc-implement больше не нужны,
// kube-ovn управляет underlay сам).
