// Package service — NetworkInterface (NIC) use-cases (public CRUD/Operations +
// Attach/Detach) и internal-проекция (InternalNetworkInterfaceService).
//
// NIC — first-class сетевой интерфейс (AWS-ENI-style; epic KAC-2). Принадлежит
// подсети (и транзитивно сети/VPN), несёт один primary private IPv4. Публичный
// NetworkInterface — lean; data-plane-поля (hv_id/sid/...) — только в internal-проекции.
//
// MVP-ограничение v1: primary_v4_address ОБЯЗАТЕЛЕН в спеке (авто-аллокация из
// CIDR подсети через VPC IPAM + появление в ListUsedAddresses — follow-up).
package service

import (
	"context"
	"fmt"
	"strings"
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

// NetworkInterfaceFilter — фильтр для List.
type NetworkInterfaceFilter struct {
	FolderID   string
	InstanceID string
	SubnetID   string
	NetworkID  string
}

// NetworkInterfaceRepo — port-интерфейс репозитория NIC.
type NetworkInterfaceRepo interface {
	Get(ctx context.Context, id string) (*domain.NetworkInterface, error)
	List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*domain.NetworkInterface, string, error)
	ListByHypervisor(ctx context.Context, hvID string) ([]*domain.NetworkInterface, error)
	Insert(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error)
	UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error)
	SetInstance(ctx context.Context, id, instanceID, niIndex string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterface, error)
	SetDataplane(ctx context.Context, id string, dp domain.NICDataplane, newStatus domain.NetworkInterfaceStatus, setStatus bool) (*domain.NetworkInterface, bool, error)
	Delete(ctx context.Context, id string) error
}

const niResource = "network interface"

func niResourceID(id string) error { return corevalidate.ResourceID(niResource, ids.PrefixSubnet, id) }

// ---- public NetworkInterfaceService ----

// CreateNICReq — запрос на создание NIC.
type CreateNICReq struct {
	FolderID             string
	Name                 string
	Description          string
	Labels               map[string]string
	SubnetID             string
	PrimaryV4Address     string // v1: обязателен
	SecondaryV4Addresses []string
	V6Addresses          []string
	SecurityGroupIDs     []string
	InstanceID           string // опц. — сразу приаттачить
	Index                string
}

// UpdateNICReq — частичное обновление NIC.
type UpdateNICReq struct {
	ID                   string
	Name                 string
	Description          string
	Labels               map[string]string
	SecurityGroupIDs     []string
	SecondaryV4Addresses []string
	V6Addresses          []string
	UpdateMask           []string
}

// NetworkInterfaceService — бизнес-логика управления NIC.
type NetworkInterfaceService struct {
	repo         NetworkInterfaceRepo
	subnetRepo   SubnetRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewNetworkInterfaceService создаёт NetworkInterfaceService.
func NewNetworkInterfaceService(repo NetworkInterfaceRepo, subnetRepo SubnetRepo, folderClient FolderClient, opsRepo operations.Repo) *NetworkInterfaceService {
	return &NetworkInterfaceService{repo: repo, subnetRepo: subnetRepo, folderClient: folderClient, opsRepo: opsRepo}
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
	if strings.TrimSpace(req.PrimaryV4Address) == "" {
		// v1: авто-аллокация ещё не реализована — адрес обязателен.
		return nil, status.Error(codes.InvalidArgument, "primary_v4_address_spec.address is required (auto-allocation not yet implemented)")
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
	sub, err := s.subnetRepo.Get(ctx, req.SubnetID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	st := domain.NIStatusAvailable
	if req.InstanceID != "" {
		st = domain.NIStatusActive
	}
	n := &domain.NetworkInterface{
		ID:                   niID,
		FolderID:             req.FolderID,
		CreatedAt:            time.Now().UTC(),
		Name:                 req.Name,
		Description:          req.Description,
		Labels:               req.Labels,
		SubnetID:             req.SubnetID,
		NetworkID:            sub.NetworkID,
		PrimaryV4Address:     strings.TrimSpace(req.PrimaryV4Address),
		SecondaryV4Addresses: req.SecondaryV4Addresses,
		V6Addresses:          req.V6Addresses,
		SecurityGroupIDs:     req.SecurityGroupIDs,
		InstanceID:           req.InstanceID,
		Index:                req.Index,
		Status:               st,
	}
	created, err := s.repo.Insert(ctx, n)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return anypb.New(protoconv.NetworkInterface(created))
}

// Update обновляет name/description/labels/security_group_ids.
func (s *NetworkInterfaceService) Update(ctx context.Context, req UpdateNICReq) (*operations.Operation, error) {
	if err := niResourceID(req.ID); err != nil {
		return nil, err
	}
	known := map[string]struct{}{"name": {}, "description": {}, "labels": {}, "security_group_ids": {}, "secondary_v4_addresses": {}, "v6_addresses": {}}
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
		applyNICMask(n, req)
		updated, err := s.repo.UpdateMeta(ctx, n)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.NetworkInterface(updated))
	})
	return &op, nil
}

func applyNICMask(n *domain.NetworkInterface, req UpdateNICReq) {
	if len(req.UpdateMask) == 0 {
		n.Name, n.Description, n.Labels, n.SecurityGroupIDs = req.Name, req.Description, req.Labels, req.SecurityGroupIDs
		n.SecondaryV4Addresses, n.V6Addresses = req.SecondaryV4Addresses, req.V6Addresses
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
		case "secondary_v4_addresses":
			n.SecondaryV4Addresses = req.SecondaryV4Addresses
		case "v6_addresses":
			n.V6Addresses = req.V6Addresses
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
		if cur.InstanceID != "" {
			return nil, status.Errorf(codes.FailedPrecondition, "network interface %s is still attached to instance %s; detach it first", id, cur.InstanceID)
		}
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
		if cur.InstanceID != "" && cur.InstanceID != instanceID {
			return nil, status.Errorf(codes.FailedPrecondition, "network interface %s is already attached to instance %s", id, cur.InstanceID)
		}
		idx := index
		if idx == "" {
			idx = "0"
		}
		updated, err := s.repo.SetInstance(ctx, id, instanceID, idx, domain.NIStatusActive)
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
		updated, err := s.repo.SetInstance(ctx, id, "", "", domain.NIStatusAvailable)
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
