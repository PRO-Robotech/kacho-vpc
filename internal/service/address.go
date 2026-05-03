package service

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// AddressService реализует use-cases для Address.
type AddressService struct {
	repo         AddressRepo
	opsRepo      operations.Repo
	folderClient FolderClient
}

// NewAddressService создаёт AddressService.
func NewAddressService(r AddressRepo, ops operations.Repo, fc FolderClient) *AddressService {
	return &AddressService{repo: r, opsRepo: ops, folderClient: fc}
}

// Get возвращает Address по ID (синхронный).
func (s *AddressService) Get(ctx context.Context, id string) (*domain.Address, error) {
	if id == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("address_id", "address_id is required").Err()
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, coreerrors.NotFound("Address", id).Err()
	}
	return a, nil
}

// List возвращает список Address (синхронный).
func (s *AddressService) List(ctx context.Context, filter ListFilter) ([]domain.Address, string, error) {
	return s.repo.List(ctx, filter)
}

// Create создаёт Address асинхронно с выделением IP из 203.0.113.0/24.
func (s *AddressService) Create(ctx context.Context, folderID, name, description string, labels map[string]string, addressType string, zoneID string) (*operations.Operation, error) {
	if folderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if addressType != "" && addressType != "ADDRESS_TYPE_EXTERNAL" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("address_type", "only ADDRESS_TYPE_EXTERNAL is supported in phase 1.0").Err()
	}

	resourceID := ids.NewUID()
	op, err := operations.New("Create Address "+name, &pb.CreateAddressMetadata{AddressId: resourceID})
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

		a := &domain.Address{
			ID:          resourceID,
			FolderID:    folderID,
			Name:        name,
			Description: description,
			Labels:      labels,
			AddressType: "ADDRESS_TYPE_EXTERNAL",
			ZoneID:      zoneID,
			Status:      domain.AddressStatusReserved,
		}

		// Выделяем псевдо-случайный IP из 203.0.113.0/24 (TEST-NET-3, RFC 5737)
		// Retry max 10 при UNIQUE violation.
		const maxRetries = 10
		for i := 0; i < maxRetries; i++ {
			octet := rand.Intn(254) + 1 //nolint:gosec
			a.AllocatedIPv4 = fmt.Sprintf("203.0.113.%d", octet)
			cerr := s.repo.Create(ctx, a)
			if cerr == nil {
				break
			}
			if isUniqueViolation(cerr) {
				if i == maxRetries-1 {
					return nil, coreerrors.Aborted("failed to allocate IP address after retries").Err()
				}
				continue
			}
			return nil, cerr
		}

		final, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainAddressToProto(final))
	})

	return &op, nil
}

// Update обновляет Address асинхронно.
func (s *AddressService) Update(ctx context.Context, addressID, name, description string, labels map[string]string, updateMask []string) (*operations.Operation, error) {
	if addressID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("address_id", "address_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("Address", addressID).Err()
	}

	op, err := operations.New("Update Address "+addressID, &pb.UpdateAddressMetadata{AddressId: addressID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		cur, gerr := s.repo.Get(ctx, addressID)
		if gerr != nil {
			return nil, gerr
		}
		if cur == nil {
			return nil, status.Errorf(codes.NotFound, "Address %s not found", addressID)
		}

		applyAddressUpdateMask(cur, name, description, labels, updateMask)
		if uerr := s.repo.Update(ctx, cur); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, addressID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainAddressToProto(final))
	})

	return &op, nil
}

// Delete удаляет Address асинхронно.
func (s *AddressService) Delete(ctx context.Context, addressID string) (*operations.Operation, error) {
	if addressID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("address_id", "address_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("Address", addressID).Err()
	}

	op, err := operations.New("Delete Address "+addressID, &pb.DeleteAddressMetadata{AddressId: addressID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if derr := s.repo.SoftDelete(ctx, addressID); derr != nil {
			return nil, derr
		}
		return anypb.New(&pb.DeleteAddressMetadata{AddressId: addressID})
	})

	return &op, nil
}

func applyAddressUpdateMask(a *domain.Address, name, description string, labels map[string]string, mask []string) {
	if len(mask) == 0 {
		a.Name = name
		a.Description = description
		a.Labels = labels
		return
	}
	for _, f := range mask {
		switch f {
		case "name":
			a.Name = name
		case "description":
			a.Description = description
		case "labels":
			a.Labels = labels
		}
	}
}

func domainAddressToProto(a *domain.Address) *pb.Address {
	proto := &pb.Address{
		Id:            a.ID,
		FolderId:      a.FolderID,
		Name:          a.Name,
		Description:   a.Description,
		Labels:        a.Labels,
		AddressType:   addressTypeProto(a.AddressType),
		ZoneId:        a.ZoneID,
		AllocatedIpv4: a.AllocatedIPv4,
		Status:        pb.AddressStatus(a.Status),
	}
	if !a.CreatedAt.IsZero() {
		proto.CreatedAt = timestampProto(a.CreatedAt)
	}
	return proto
}

func addressTypeProto(t string) pb.AddressType {
	if strings.EqualFold(t, "ADDRESS_TYPE_EXTERNAL") || strings.EqualFold(t, "EXTERNAL") {
		return pb.AddressType_ADDRESS_TYPE_EXTERNAL
	}
	return pb.AddressType_ADDRESS_TYPE_UNSPECIFIED
}

// isUniqueViolation определяет является ли ошибка нарушением UNIQUE constraint.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") ||
		strings.Contains(msg, "unique_violation") ||
		strings.Contains(msg, "duplicate key")
}
