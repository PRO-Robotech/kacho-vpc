package service

import (
	"context"
	"net"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// NetworkService реализует use-cases для Network.
type NetworkService struct {
	repo         NetworkRepo
	opsRepo      operations.Repo
	folderClient FolderClient
}

// NewNetworkService создаёт NetworkService.
func NewNetworkService(r NetworkRepo, ops operations.Repo, fc FolderClient) *NetworkService {
	return &NetworkService{repo: r, opsRepo: ops, folderClient: fc}
}

// Get возвращает Network по ID (синхронный).
func (s *NetworkService) Get(ctx context.Context, id string) (*domain.Network, error) {
	if id == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("network_id", "network_id is required").Err()
	}
	n, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, coreerrors.NotFound("Network", id).Err()
	}
	return n, nil
}

// List возвращает список Network по folder_id (синхронный).
func (s *NetworkService) List(ctx context.Context, filter ListFilter) ([]domain.Network, string, error) {
	return s.repo.List(ctx, filter)
}

// Create создаёт Network асинхронно — возвращает Operation.
func (s *NetworkService) Create(ctx context.Context, folderID, name, description string, labels map[string]string) (*operations.Operation, error) {
	if folderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}

	resourceID := ids.NewUID()
	op, err := operations.New("Create Network "+name, &pb.CreateNetworkMetadata{NetworkId: resourceID})
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

		n := &domain.Network{
			ID:          resourceID,
			FolderID:    folderID,
			Name:        name,
			Description: description,
			Labels:      labels,
			Status:      domain.NetworkStatusProvisioning,
			Generation:  1,
		}
		if cerr := s.repo.Create(ctx, n); cerr != nil {
			return nil, cerr
		}

		created, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		// Переводим в ACTIVE
		created.Status = domain.NetworkStatusActive
		if uerr := s.repo.Update(ctx, created); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainNetworkToProto(final))
	})

	return &op, nil
}

// Update обновляет Network асинхронно — возвращает Operation.
func (s *NetworkService) Update(ctx context.Context, networkID, resourceVersion, name, description string, labels map[string]string, updateMask []string) (*operations.Operation, error) {
	if networkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("network_id", "network_id is required").Err()
	}

	// Быстрая синхронная проверка существования
	existing, err := s.repo.Get(ctx, networkID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("Network", networkID).Err()
	}

	op, err := operations.New("Update Network "+networkID, &pb.UpdateNetworkMetadata{NetworkId: networkID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		cur, gerr := s.repo.Get(ctx, networkID)
		if gerr != nil {
			return nil, gerr
		}
		if cur == nil {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", networkID)
		}
		// OCC check
		if resourceVersion != "" && cur.ResourceVersion != resourceVersion {
			return nil, status.Errorf(codes.Aborted, "resource_version mismatch: expected %s, got %s", resourceVersion, cur.ResourceVersion)
		}

		applyNetworkUpdateMask(cur, name, description, labels, updateMask)
		cur.Generation++
		if uerr := s.repo.Update(ctx, cur); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, networkID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainNetworkToProto(final))
	})

	return &op, nil
}

// Delete удаляет Network асинхронно — возвращает Operation.
func (s *NetworkService) Delete(ctx context.Context, networkID string) (*operations.Operation, error) {
	if networkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("network_id", "network_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, networkID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("Network", networkID).Err()
	}

	op, err := operations.New("Delete Network "+networkID, &pb.DeleteNetworkMetadata{NetworkId: networkID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		has, herr := s.repo.HasDependents(ctx, networkID)
		if herr != nil {
			return nil, herr
		}
		if has {
			return nil, status.Errorf(codes.FailedPrecondition,
				"Network %s has dependent resources (Subnet/SecurityGroup/RouteTable)", networkID)
		}
		if derr := s.repo.SoftDelete(ctx, networkID); derr != nil {
			return nil, derr
		}
		return anypb.New(&pb.DeleteNetworkMetadata{NetworkId: networkID})
	})

	return &op, nil
}

// applyNetworkUpdateMask применяет поля из update_mask (или все если маска пустая).
func applyNetworkUpdateMask(n *domain.Network, name, description string, labels map[string]string, mask []string) {
	if len(mask) == 0 {
		n.Name = name
		n.Description = description
		n.Labels = labels
		return
	}
	for _, f := range mask {
		switch f {
		case "name":
			n.Name = name
		case "description":
			n.Description = description
		case "labels":
			n.Labels = labels
		}
	}
}

// domainNetworkToProto конвертирует domain.Network в proto.
func domainNetworkToProto(n *domain.Network) *pb.Network {
	proto := &pb.Network{
		Id:              n.ID,
		FolderId:        n.FolderID,
		Name:            n.Name,
		Description:     n.Description,
		Labels:          n.Labels,
		Status:          pb.NetworkStatus(n.Status),
		Generation:      n.Generation,
		ResourceVersion: n.ResourceVersion,
	}
	if !n.CreatedAt.IsZero() {
		proto.CreatedAt = timestampProto(n.CreatedAt)
	}
	if !n.StatusLastTransitionAt.IsZero() {
		proto.StatusLastTransitionAt = timestampProto(n.StatusLastTransitionAt)
	}
	return proto
}

// validateCIDR проверяет CIDR-блок: парсинг + отсутствие host-bits.
func validateCIDR(cidr string) error {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid CIDR %q: %v", cidr, err)
	}
	if !ip.Equal(ipnet.IP) {
		return status.Errorf(codes.InvalidArgument, "CIDR %q has host bits set (use %s)", cidr, ipnet.String())
	}
	return nil
}
