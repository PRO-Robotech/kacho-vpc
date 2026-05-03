package service

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// CreateAddressReq — запрос на создание адреса.
type CreateAddressReq struct {
	FolderID           string
	Name               string
	Description        string
	Labels             map[string]string
	DeletionProtection bool
	// Для external (если ExternalSpec != nil):
	ExternalSpec *ExternalAddrSpec
	// Для internal (если InternalSpec != nil):
	InternalSpec *InternalAddrSpec
}

// ExternalAddrSpec — спецификация внешнего адреса.
type ExternalAddrSpec struct {
	Address string
	ZoneID  string
}

// InternalAddrSpec — спецификация внутреннего адреса.
type InternalAddrSpec struct {
	Address  string
	SubnetID string
}

// UpdateAddressReq — запрос на обновление адреса.
type UpdateAddressReq struct {
	AddressID          string
	Name               string
	Description        string
	Labels             map[string]string
	DeletionProtection bool
	Reserved           bool
	UpdateMask         []string
}

// AddressService — бизнес-логика управления IP-адресами.
type AddressService struct {
	repo         AddressRepo
	subnetRepo   SubnetRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewAddressService создаёт AddressService.
func NewAddressService(repo AddressRepo, subnetRepo SubnetRepo, folderClient FolderClient, opsRepo operations.Repo) *AddressService {
	return &AddressService{repo: repo, subnetRepo: subnetRepo, folderClient: folderClient, opsRepo: opsRepo}
}

// Get возвращает Address по ID.
func (s *AddressService) Get(ctx context.Context, id string) (*domain.Address, error) {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return a, nil
}

// List возвращает список адресов.
func (s *AddressService) List(ctx context.Context, f AddressFilter, p Pagination) ([]*domain.Address, string, error) {
	return s.repo.List(ctx, f, p)
}

// Create инициирует создание Address.
func (s *AddressService) Create(ctx context.Context, req CreateAddressReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.ExternalSpec == nil && req.InternalSpec == nil {
		return nil, status.Error(codes.InvalidArgument, "address_spec required")
	}

	addrID := ids.NewUID()
	op, err := operations.New(
		"vpc",
		fmt.Sprintf("Create address %s", req.Name),
		&vpcv1.CreateAddressMetadata{AddressId: addrID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doCreate(ctx, addrID, req)
	})

	return &op, nil
}

func (s *AddressService) doCreate(ctx context.Context, addrID string, req CreateAddressReq) (*anypb.Any, error) {
	exists, err := s.folderClient.Exists(ctx, req.FolderID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "folder %s not found", req.FolderID)
	}

	a := &domain.Address{
		ID:                 addrID,
		FolderID:           req.FolderID,
		CreatedAt:          time.Now().UTC(),
		Name:               req.Name,
		Description:        req.Description,
		Labels:             req.Labels,
		DeletionProtection: req.DeletionProtection,
		Reserved:           true,
	}

	if req.ExternalSpec != nil {
		a.Type = domain.AddressTypeExternal
		a.IpVersion = domain.IpVersionIPv4
		ipAddr := req.ExternalSpec.Address
		if ipAddr == "" {
			ipAddr, err = s.allocateExternalIP(ctx)
			if err != nil {
				return nil, err
			}
		}
		a.ExternalIpv4 = &domain.ExternalIpv4Spec{
			Address: ipAddr,
			ZoneID:  req.ExternalSpec.ZoneID,
		}
	} else {
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv4
		ipAddr := req.InternalSpec.Address
		subnetID := req.InternalSpec.SubnetID
		if ipAddr == "" && subnetID != "" {
			sub, serr := s.subnetRepo.Get(ctx, subnetID)
			if serr != nil {
				return nil, status.Errorf(codes.NotFound, "subnet %s not found", subnetID)
			}
			ipAddr, err = s.allocateInternalIP(ctx, sub)
			if err != nil {
				return nil, err
			}
		}
		a.InternalIpv4 = &domain.InternalIpv4Spec{
			Address:  ipAddr,
			SubnetID: subnetID,
		}
	}

	created, err := s.repo.Insert(ctx, a)
	if err != nil {
		return nil, err
	}
	return anypb.New(domainAddressToProto(created))
}

// allocateExternalIP выбирает случайный IP из 203.0.113.0/24 (TEST-NET-3 RFC 5737).
func (s *AddressService) allocateExternalIP(ctx context.Context) (string, error) {
	const maxAttempts = 10
	_, network, _ := net.ParseCIDR("203.0.113.0/24")
	baseIP := binary.BigEndian.Uint32(network.IP)
	ones, bits := network.Mask.Size()
	hostBits := bits - ones
	maxHosts := uint32(1<<hostBits) - 2 // исключаем .0 и .255

	for i := 0; i < maxAttempts; i++ {
		offset := uint32(rand.Intn(int(maxHosts))) + 1
		ipInt := baseIP + offset
		var ipBytes [4]byte
		binary.BigEndian.PutUint32(ipBytes[:], ipInt)
		ip := net.IP(ipBytes[:]).String()

		alreadyExists, err := s.repo.ExistsIP(ctx, ip)
		if err != nil {
			return "", err
		}
		if !alreadyExists {
			return ip, nil
		}
	}
	return "", status.Error(codes.ResourceExhausted, "cannot allocate external IP: pool exhausted")
}

// allocateInternalIP выбирает случайный IP из первого CIDR подсети.
func (s *AddressService) allocateInternalIP(ctx context.Context, sub *domain.Subnet) (string, error) {
	if len(sub.V4CidrBlocks) == 0 {
		return "", status.Error(codes.FailedPrecondition, "subnet has no cidr blocks")
	}
	const maxAttempts = 10
	_, network, err := net.ParseCIDR(sub.V4CidrBlocks[0])
	if err != nil {
		return "", status.Errorf(codes.Internal, "parse subnet cidr: %v", err)
	}
	baseIP := binary.BigEndian.Uint32(network.IP)
	ones, bits := network.Mask.Size()
	hostBits := bits - ones
	maxHosts := uint32(1<<hostBits) - 2
	if maxHosts == 0 {
		return "", status.Error(codes.FailedPrecondition, "subnet too small")
	}

	for i := 0; i < maxAttempts; i++ {
		offset := uint32(rand.Intn(int(maxHosts))) + 1
		ipInt := baseIP + offset
		var ipBytes [4]byte
		binary.BigEndian.PutUint32(ipBytes[:], ipInt)
		ip := net.IP(ipBytes[:]).String()

		alreadyExists, err := s.repo.ExistsIP(ctx, ip)
		if err != nil {
			return "", err
		}
		if !alreadyExists {
			return ip, nil
		}
	}
	return "", status.Error(codes.ResourceExhausted, "cannot allocate internal IP: subnet exhausted")
}

// Update обновляет Address.
func (s *AddressService) Update(ctx context.Context, req UpdateAddressReq) (*operations.Operation, error) {
	if req.AddressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}

	op, err := operations.New(
		"vpc",
		fmt.Sprintf("Update address %s", req.AddressID),
		&vpcv1.UpdateAddressMetadata{AddressId: req.AddressID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return s.doUpdate(ctx, req)
	})

	return &op, nil
}

func (s *AddressService) doUpdate(ctx context.Context, req UpdateAddressReq) (*anypb.Any, error) {
	a, err := s.repo.Get(ctx, req.AddressID)
	if err != nil {
		return nil, mapRepoErr(err)
	}

	applyAddressMask(a, req)

	updated, err := s.repo.Update(ctx, a)
	if err != nil {
		return nil, err
	}
	return anypb.New(domainAddressToProto(updated))
}

func applyAddressMask(a *domain.Address, req UpdateAddressReq) {
	if len(req.UpdateMask) == 0 {
		a.Name = req.Name
		a.Description = req.Description
		a.Labels = req.Labels
		a.DeletionProtection = req.DeletionProtection
		a.Reserved = req.Reserved
		return
	}
	for _, field := range req.UpdateMask {
		switch field {
		case "name":
			a.Name = req.Name
		case "description":
			a.Description = req.Description
		case "labels":
			a.Labels = req.Labels
		case "deletion_protection":
			a.DeletionProtection = req.DeletionProtection
		case "reserved":
			a.Reserved = req.Reserved
		}
	}
}

// Delete удаляет Address.
func (s *AddressService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}

	op, err := operations.New(
		"vpc",
		fmt.Sprintf("Delete address %s", id),
		&vpcv1.DeleteAddressMetadata{AddressId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if err := s.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&vpcv1.DeleteAddressMetadata{AddressId: id})
	})

	return &op, nil
}

// domainAddressToProto конвертирует domain Address в proto Address.
func domainAddressToProto(a *domain.Address) *vpcv1.Address {
	p := &vpcv1.Address{
		Id:                 a.ID,
		FolderId:           a.FolderID,
		Name:               a.Name,
		Description:        a.Description,
		Labels:             a.Labels,
		Reserved:           a.Reserved,
		Used:               a.Used,
		Type:               vpcv1.Address_Type(a.Type),
		IpVersion:          vpcv1.Address_IpVersion(a.IpVersion),
		DeletionProtection: a.DeletionProtection,
	}
	if a.ExternalIpv4 != nil {
		p.Address = &vpcv1.Address_ExternalIpv4Address{
			ExternalIpv4Address: &vpcv1.ExternalIpv4Address{
				Address: a.ExternalIpv4.Address,
				ZoneId:  a.ExternalIpv4.ZoneID,
			},
		}
	} else if a.InternalIpv4 != nil {
		p.Address = &vpcv1.Address_InternalIpv4Address{
			InternalIpv4Address: &vpcv1.InternalIpv4Address{
				Address: a.InternalIpv4.Address,
				Scope: &vpcv1.InternalIpv4Address_SubnetId{
					SubnetId: a.InternalIpv4.SubnetID,
				},
			},
		}
	}
	return p
}
