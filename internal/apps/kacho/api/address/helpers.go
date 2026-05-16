package address

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
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует трансферы Address/time через init() (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// niReferrerType — ReferrerType в address_references для адресов, привязанных
// к NetworkInterface. Mirror внутри use-case-пакета (zonded copy от
// `internal/service/network_interface.go::niReferrerType`).
const niReferrerType = "network_interface"

// mapRepoErr — переводит repo-sentinel в gRPC status. Логика идентична
// `service.mapRepoErr`; live-копия здесь нужна, потому что та функция лежит в
// другом пакете и непублична. Wave 3 после полного переноса всех use-case'ов
// извлечём общий maperr в shared-leaf или в `internal/apps/kacho`.
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

// isUniqueViolation распознаёт UNIQUE-violation для retry-loop в allocate.
//
// Принципиальный путь: repo через wrapPgErr оборачивает SQLSTATE 23505 в
// ErrAlreadyExists — это и есть contract repo↔use-case. Substring-fallback
// оставлен для случаев когда какой-то новый repo может вернуть raw pgErr
// без обёртки (defensive). Constraint-specific имена удалены — use-case не
// должен знать DB-schema.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, repo.ErrAlreadyExists) {
		return true
	}
	// Defensive fallback: общие признаки UNIQUE-violation без leak'а
	// constraint-имён в use-case.
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") ||
		strings.Contains(msg, "duplicate key value")
}

// marshalAddressRecord конвертирует repo-entity Address в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4). Используется worker'ами Create/
// Update/Move для запихивания результата в Operation.response.
func marshalAddressRecord(rec *kachorepo.AddressRecord) (*anypb.Any, error) {
	var dst *vpcv1.Address
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Address: %w", err)
	}
	return anypb.New(dst)
}

// addressPayloadMap — snapshot Address для outbox payload. A.7 sub-PR 2
// (KAC-94): CQRS-миграция Address — outbox-emit идёт из use-case-кода через
// `w.Outbox().Emit(...)`, поэтому payload-snapshot нужен здесь.
// Семантика — JSON round-trip, parity с `addressPayload` в
// `internal/repo/outbox.go` для legacy AddressRepo writes (которые остаются
// до полного переезда NIC/AP/Allocate на CQRS).
func addressPayloadMap(a *kachorepo.AddressRecord) map[string]any {
	b, err := json.Marshal(a)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}
