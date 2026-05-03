package service

import (
	"context"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// SubnetService реализует use-cases для Subnet.
type SubnetService struct {
	repo         SubnetRepo
	networkRepo  NetworkRepo
	opsRepo      operations.Repo
	folderClient FolderClient
}

// NewSubnetService создаёт SubnetService.
func NewSubnetService(r SubnetRepo, nr NetworkRepo, ops operations.Repo, fc FolderClient) *SubnetService {
	return &SubnetService{repo: r, networkRepo: nr, opsRepo: ops, folderClient: fc}
}

// Get возвращает Subnet по ID (синхронный).
func (s *SubnetService) Get(ctx context.Context, id string) (*domain.Subnet, error) {
	if id == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("subnet_id", "subnet_id is required").Err()
	}
	sub, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, coreerrors.NotFound("Subnet", id).Err()
	}
	return sub, nil
}

// List возвращает список Subnet (синхронный).
func (s *SubnetService) List(ctx context.Context, filter ListFilter) ([]domain.Subnet, string, error) {
	return s.repo.List(ctx, filter)
}

// Create создаёт Subnet асинхронно.
func (s *SubnetService) Create(ctx context.Context, folderID, networkID, zoneID, cidrBlock, name, description string, labels map[string]string) (*operations.Operation, error) {
	if folderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if networkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("network_id", "network_id is required").Err()
	}
	if name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if cidrBlock == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("cidr_block", "cidr_block is required").Err()
	}
	if err := validateCIDR(cidrBlock); err != nil {
		return nil, err
	}

	resourceID := ids.NewUID()
	op, err := operations.New("Create Subnet "+name, &pb.CreateSubnetMetadata{SubnetId: resourceID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, ferr := s.folderClient.Exists(ctx, folderID)
		if ferr != nil {
			return nil, ferr
		}
		if !exists {
			return nil, status.Errorf(codes.FailedPrecondition, "Folder %s not found", folderID)
		}

		net, nerr := s.networkRepo.Get(ctx, networkID)
		if nerr != nil {
			return nil, nerr
		}
		if net == nil {
			return nil, status.Errorf(codes.FailedPrecondition, "Network %s not found", networkID)
		}

		sub := &domain.Subnet{
			ID:          resourceID,
			FolderID:    folderID,
			NetworkID:   networkID,
			ZoneID:      zoneID,
			CIDRBlock:   cidrBlock,
			Name:        name,
			Description: description,
			Labels:      labels,
			Status:      domain.SubnetStatusProvisioning,
			Generation:  1,
		}
		if cerr := s.repo.Create(ctx, sub); cerr != nil {
			return nil, cerr
		}

		created, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		created.Status = domain.SubnetStatusActive
		if uerr := s.repo.Update(ctx, created); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainSubnetToProto(final))
	})

	return &op, nil
}

// Update обновляет Subnet асинхронно.
func (s *SubnetService) Update(ctx context.Context, subnetID, resourceVersion, name, description string, labels map[string]string, updateMask []string) (*operations.Operation, error) {
	if subnetID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("subnet_id", "subnet_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, subnetID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("Subnet", subnetID).Err()
	}

	op, err := operations.New("Update Subnet "+subnetID, &pb.UpdateSubnetMetadata{SubnetId: subnetID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		cur, gerr := s.repo.Get(ctx, subnetID)
		if gerr != nil {
			return nil, gerr
		}
		if cur == nil {
			return nil, status.Errorf(codes.NotFound, "Subnet %s not found", subnetID)
		}
		if resourceVersion != "" && cur.ResourceVersion != resourceVersion {
			return nil, status.Errorf(codes.Aborted, "resource_version mismatch: expected %s, got %s", resourceVersion, cur.ResourceVersion)
		}

		applySubnetUpdateMask(cur, name, description, labels, updateMask)
		cur.Generation++
		if uerr := s.repo.Update(ctx, cur); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, subnetID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainSubnetToProto(final))
	})

	return &op, nil
}

// Delete удаляет Subnet асинхронно.
func (s *SubnetService) Delete(ctx context.Context, subnetID string) (*operations.Operation, error) {
	if subnetID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("subnet_id", "subnet_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, subnetID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("Subnet", subnetID).Err()
	}

	op, err := operations.New("Delete Subnet "+subnetID, &pb.DeleteSubnetMetadata{SubnetId: subnetID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if derr := s.repo.SoftDelete(ctx, subnetID); derr != nil {
			return nil, derr
		}
		return anypb.New(&pb.DeleteSubnetMetadata{SubnetId: subnetID})
	})

	return &op, nil
}

func applySubnetUpdateMask(s *domain.Subnet, name, description string, labels map[string]string, mask []string) {
	if len(mask) == 0 {
		s.Name = name
		s.Description = description
		s.Labels = labels
		return
	}
	for _, f := range mask {
		switch f {
		case "name":
			s.Name = name
		case "description":
			s.Description = description
		case "labels":
			s.Labels = labels
		}
	}
}

func domainSubnetToProto(s *domain.Subnet) *pb.Subnet {
	proto := &pb.Subnet{
		Id:              s.ID,
		FolderId:        s.FolderID,
		NetworkId:       s.NetworkID,
		ZoneId:          s.ZoneID,
		CidrBlock:       s.CIDRBlock,
		Name:            s.Name,
		Description:     s.Description,
		Labels:          s.Labels,
		Status:          pb.SubnetStatus(s.Status),
		Generation:      s.Generation,
		ResourceVersion: s.ResourceVersion,
	}
	if !s.CreatedAt.IsZero() {
		proto.CreatedAt = timestampProto(s.CreatedAt)
	}
	if !s.StatusLastTransitionAt.IsZero() {
		proto.StatusLastTransitionAt = timestampProto(s.StatusLastTransitionAt)
	}
	return proto
}
