package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// Blank-import регистрирует трансферы Network/time через init() (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// networkPayloadMap — snapshot Network для outbox payload. Wave 5 (KAC-94)
// CQRS: writer.Outbox().Emit принимает map[string]any (а legacy repo делал
// snapshot внутри Insert). Здесь — JSON round-trip как networkPayload в
// `internal/repo/outbox.go` (legacy).
func networkPayloadMap(n *domain.NetworkRecord) map[string]any {
	b, err := json.Marshal(n)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// mapRepoErr — переводит repo-sentinel в gRPC status. Логика идентична
// `service.mapRepoErr`; live-копия здесь нужна, потому что та функция лежит в
// другом пакете и непублична. Wave 3b после переноса всех use-case'ов из
// `internal/service` извлечём общий maperr в shared-leaf или в `internal/apps/kacho`.
//
// Sentinel-prefix (`failed precondition: `, `not found`, ...) удаляется при
// преобразовании в gRPC-сообщение, чтобы клиент видел verbatim YC text без
// internal-обёртки.
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

// stripSentinel — извлекает «полезную» часть сообщения (после «sentinel: »).
func stripSentinel(err, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		return rest
	}
	return msg
}

// checkFolderExists — verbatim YC sync precondition: destination folder must
// exist. Параллельный к `service.checkFolderExists`; live-копия — пока другие
// use-case'ы не переехали в `internal/apps/kacho`. См. kacho-vpc#8.
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

// checkMoveDestination — sync precondition для Move: dest должен отличаться от
// source и существовать. См. kacho-vpc#10.
func checkMoveDestination(ctx context.Context, fc FolderClient, currentFolderID, destFolderID string) error {
	if destFolderID == currentFolderID {
		return status.Error(codes.InvalidArgument, "Illegal argument Destination folder is the same as the source")
	}
	return checkFolderExists(ctx, fc, destFolderID)
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

// marshalNetworkRecord конвертирует repo-entity Network в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4). Используется worker'ами Create/
// Update/Move для запихивания результата в Operation.response.
func marshalNetworkRecord(rec *domain.NetworkRecord) (*anypb.Any, error) {
	var dst *vpcv1.Network
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Network: %w", err)
	}
	return anypb.New(dst)
}
