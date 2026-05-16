package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует трансферы Gateway/time через init() (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// mapRepoErr — переводит repo-sentinel в gRPC status. Параллельный к
// `service.mapRepoErr`; live-копия здесь нужна, потому что та функция лежит в
// другом пакете и непублична. Wave 3b: после переноса всех use-case'ов
// извлечём общий maperr в shared-leaf или в `internal/apps/kacho`.
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

// checkMoveDestination — sync precondition для Move: dest должен отличаться от
// source. См. kacho-vpc#10.
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

// marshalGatewayRecord конвертирует repo-entity Gateway в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4). Используется worker'ами для запихивания
// результата в Operation.response.
func marshalGatewayRecord(rec *kacho.GatewayRecord) (*anypb.Any, error) {
	var dst *vpcv1.Gateway
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Gateway: %w", err)
	}
	return anypb.New(dst)
}

// gatewayPayloadMap — payload snapshot для outbox-event (parity с legacy
// `repo.gatewayPayload`). Использует exported shim `helpers.GatewayPayload` —
// иначе пришлось бы дублировать map-encoding здесь.
func gatewayPayloadMap(g *kacho.GatewayRecord) map[string]any {
	return helpers.GatewayPayload(g)
}
