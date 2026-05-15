package networkinterface

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// Blank-import регистрирует трансферы (включая NetworkInterface) через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// niResource — название ресурса для сообщений `corevalidate.ResourceID`.
const niResource = "network interface"

// niResourceID — sync-валидация формата NIC-id (3-char crockford-base32 prefix
// + 17-char base32). Для NIC переиспользуется `ids.PrefixSubnet` (см. workspace
// CLAUDE.md §3 / `kacho-vpc/CLAUDE.md` §3 Resource ID format) — это не баг, а
// сознательное переиспользование префикса `e9b` (Subnet/Address/NIC).
func niResourceID(id string) error {
	return corevalidate.ResourceID(niResource, ids.PrefixSubnet, id)
}

// mapRepoErr — переводит repo-sentinel в gRPC status. Параллельный шаблон с
// `network`/`privateendpoint`/`gateway`/`routetable` use-case-пакетами. После
// полного выноса всех use-case'ов из `internal/service` (Wave 3 завершение)
// извлечём общий maperr в shared-leaf.
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
// exist. Параллельный к `network.checkFolderExists`.
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

// validateNICAddressCardinality fast-fail sync-валидация: на одной NetworkInterface
// разрешён максимум один IPv4 и максимум один IPv6 (KAC-55). Совпадает с DB-уровнем
// `network_interfaces_v4_addr_max1` / `_v6_addr_max1` (миграция 0018) — DB-side как
// финальный backstop, эта функция даёт понятный InvalidArgument до создания Operation.
// Multi-IP per VM реализуется через несколько NIC, не через secondary addresses в
// одном NIC (упрощённая модель vs AWS ENI; зеркалит verbatim YC compute API).
func validateNICAddressCardinality(v4IDs, v6IDs []string) error {
	if len(v4IDs) > 1 {
		return invalidArg("v4_address_ids", "at most one IPv4 address per network interface (use multiple NICs for multi-IP)")
	}
	if len(v6IDs) > 1 {
		return invalidArg("v6_address_ids", "at most one IPv6 address per network interface (use multiple NICs for multi-IP)")
	}
	return nil
}

// marshalNetworkInterfaceRecord конвертирует repo-entity NIC в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4). Используется worker'ами Create/Update/
// Attach/Detach для запихивания результата в Operation.response.
func marshalNetworkInterfaceRecord(rec *domain.NetworkInterfaceRecord) (*anypb.Any, error) {
	var dst *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer NetworkInterface: %w", err)
	}
	return anypb.New(dst)
}
