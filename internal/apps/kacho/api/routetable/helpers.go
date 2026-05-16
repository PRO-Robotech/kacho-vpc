package routetable

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует трансферы RouteTable/time через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// mapRepoErr — переводит repo-sentinel в gRPC status.
func mapRepoErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Error(codes.NotFound, stripSentinel(err, repo.ErrNotFound))
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, stripSentinel(err, repo.ErrAlreadyExists))
	case errors.Is(err, repo.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, stripSentinel(err, repo.ErrFailedPrecondition))
	case errors.Is(err, repo.ErrInvalidArg):
		return status.Error(codes.InvalidArgument, stripSentinel(err, repo.ErrInvalidArg))
	case errors.Is(err, repo.ErrInternal):
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

// checkMoveDestination — sync precondition для Move: dest должен отличаться от
// source.
//
// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
// race-prone: между sync-проверкой и async-частью folder может быть удалён
// peer-сервисом. Verbatim-YC NotFound теперь возвращается через
// `operation.error` из async-части `doMove`.
func checkMoveDestination(_ context.Context, _ FolderClient, currentFolderID, destFolderID string) error {
	if destFolderID == currentFolderID {
		return status.Error(codes.InvalidArgument, "Illegal argument Destination folder is the same as the source")
	}
	return nil
}

// invalidArg — InvalidArgument с BadRequest-details (verbatim YC parity).
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

// marshalRouteTableRecord конвертирует repo-entity RouteTable в *anypb.Any
// через DTO-реестр. Wave 5 replicate (KAC-94): принимает `*kacho.RouteTableRecord`
// (запись переехала в repo-leaf — §4 D.1).
func marshalRouteTableRecord(rec *kacho.RouteTableRecord) (*anypb.Any, error) {
	var dst *vpcv1.RouteTable
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer RouteTable: %w", err)
	}
	return anypb.New(dst)
}

// validateStaticRoutes проверяет каждую запись routes:
//   - destinationPrefix: валидный CIDR (IPv4 или IPv6) без host-bits;
//   - nextHopAddress: валидный IP-адрес (IPv4 или IPv6).
//
// Пустой массив — допустим (route table без статических маршрутов).
// При нарушении — InvalidArgument с FieldViolation `static_routes[<i>].<field>`.
func validateStaticRoutes(routes []domain.StaticRoute) error {
	for i, r := range routes {
		dpField := fmt.Sprintf("static_routes[%d].destination_prefix", i)
		if r.DestinationPrefix == "" {
			return invalidArg(dpField, dpField+" is required")
		}
		prefix, err := netip.ParsePrefix(r.DestinationPrefix)
		if err != nil {
			return invalidArg(dpField, dpField+" must be a valid CIDR (e.g. 10.0.0.0/24)")
		}
		if prefix.Masked() != prefix {
			return invalidArg(dpField,
				dpField+" must have zero host-bits (use the network address, e.g. 10.0.0.0/24, not 10.0.0.5/24)")
		}
		nhField := fmt.Sprintf("static_routes[%d].next_hop_address", i)
		if r.NextHopAddress == "" {
			return invalidArg(nhField, nhField+" is required")
		}
		if _, err := netip.ParseAddr(r.NextHopAddress); err != nil {
			return invalidArg(nhField, nhField+" must be a valid IP address (IPv4 or IPv6)")
		}
	}
	return nil
}
