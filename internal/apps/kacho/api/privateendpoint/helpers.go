package privateendpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует трансферы через init() (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// privateEndpointPayloadMap — snapshot PrivateEndpoint для outbox payload.
// Wave 5 replicate (KAC-94) CQRS: writer.Outbox().Emit принимает map[string]any
// (а legacy repo делал snapshot внутри Insert). Здесь — JSON round-trip как
// privateEndpointPayload в `internal/repo/outbox.go` (legacy).
func privateEndpointPayloadMap(pe *kacho.PrivateEndpointRecord) map[string]any {
	b, err := json.Marshal(pe)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

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

// stripSentinel удаляет sentinel-префикс из текста ошибки (verbatim YC-сообщение наружу).
func stripSentinel(err, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		return rest
	}
	return msg
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
// DTO-реестр. Wave 5 replicate (KAC-94): kachorepo.PrivateEndpointRecord —
// repo-leaf entity, DTO type-set перерегистрирован в `dto/base.go`.
func marshalPrivateEndpointRecord(rec *kacho.PrivateEndpointRecord) (*anypb.Any, error) {
	var dst *pepb.PrivateEndpoint
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer PrivateEndpoint: %w", err)
	}
	return anypb.New(dst)
}
