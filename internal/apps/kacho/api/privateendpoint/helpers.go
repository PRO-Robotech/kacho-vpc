package privateendpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	pe "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// Blank-import регистрирует трансферы через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// mapRepoErr — переводит repo-sentinel в gRPC status.
func mapRepoErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return status.Error(codes.NotFound, stripSentinel(err, ports.ErrNotFound))
	case errors.Is(err, ports.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, stripSentinel(err, ports.ErrAlreadyExists))
	case errors.Is(err, ports.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, stripSentinel(err, ports.ErrFailedPrecondition))
	case errors.Is(err, ports.ErrInvalidArg):
		return status.Error(codes.InvalidArgument, stripSentinel(err, ports.ErrInvalidArg))
	case errors.Is(err, ports.ErrInternal):
		return status.Error(codes.Internal, "internal database error")
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	return status.Error(codes.Internal, "internal database error")
}

func stripSentinel(err, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		return rest
	}
	return msg
}

// checkFolderExists — verbatim YC sync precondition.
func checkFolderExists(ctx context.Context, fc FolderClient, folderID string) error {
	exists, err := fc.Exists(ctx, folderID)
	if err != nil {
		return status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return status.Errorf(codes.NotFound, "Folder with id %s not found", folderID)
	}
	return nil
}

// invalidArg — InvalidArgument с BadRequest-details.
func invalidArg(field, desc string) error {
	st := status.New(codes.InvalidArgument, desc)
	br := &errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{
			{Field: field, Description: desc},
		},
	}
	if withDetails, derr := st.WithDetails(br); derr == nil {
		return withDetails.Err()
	}
	return st.Err()
}

// _ — invalidArg keeper (used by future Move/etc.).
var _ = invalidArg

// marshalPrivateEndpointRecord конвертирует repo-entity PE в *anypb.Any через
// DTO-реестр.
func marshalPrivateEndpointRecord(rec *domain.PrivateEndpointRecord) (*anypb.Any, error) {
	var dst *pe.PrivateEndpoint
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer PrivateEndpoint: %w", err)
	}
	return anypb.New(dst)
}
