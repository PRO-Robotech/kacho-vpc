package subnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует трансферы Subnet/Address/time через init() (skill
	// evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// subnetPayloadMap — snapshot Subnet для outbox payload. Wave 5 replicate
// (KAC-94) CQRS: writer.Outbox().Emit принимает map[string]any (а legacy repo
// делал snapshot внутри Insert). Семантика — JSON round-trip, parity с
// `subnetPayload` в `internal/repo/outbox.go`.
func subnetPayloadMap(s *kachorepo.SubnetRecord) map[string]any {
	b, err := json.Marshal(s)
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
// `service.mapRepoErr` и `network.mapRepoErr`; live-копия здесь нужна, потому что
// та функция лежит в другом пакете и непублична. Wave 3b/3c: после полного переноса
// всех use-case'ов из `internal/service` извлечём общий maperr в shared-leaf или
// в `internal/apps/kacho`.
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
func checkMoveDestination(_ context.Context, _ ProjectClient, currentProjectID, destProjectID string) error {
	if destProjectID == currentProjectID {
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

// marshalSubnetRecord конвертирует repo-entity Subnet в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4). Используется worker'ами Create/
// Update/Move/AddCidrBlocks/RemoveCidrBlocks для запихивания результата в
// Operation.response.
func marshalSubnetRecord(rec *kachorepo.SubnetRecord) (*anypb.Any, error) {
	var dst *vpcv1.Subnet
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Subnet: %w", err)
	}
	return anypb.New(dst)
}

// ---- CIDR helpers (переехали из service/validate.go и service/cidr_util.go) ----

// validateSubnetV4CIDR — host-bits=0 (canonical form) + size-limit /≤28 (verbatim
// YC, probe 2026-05-11, kacho-vpc#10). Префикс /29..32 → InvalidArgument
// "Illegal argument Invalid network prefix /<N>".
func validateSubnetV4CIDR(field, value string) error {
	if err := validateCIDRPrefix(field, value); err != nil {
		return err
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return invalidArg(field, field+" must be a valid CIDR (e.g. 10.0.0.0/24)")
	}
	if prefix.Addr().Is4() && prefix.Bits() > 28 {
		return status.Errorf(codes.InvalidArgument, "Illegal argument Invalid network prefix /%d", prefix.Bits())
	}
	return nil
}

// validateSubnetV6CIDR — host-bits=0 + проверка, что префикс реально IPv6.
func validateSubnetV6CIDR(field, value string) error {
	if err := validateCIDRPrefix(field, value); err != nil {
		return err
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return invalidArg(field, field+" must be a valid IPv6 CIDR (e.g. 2001:db8::/64)")
	}
	if !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
		return invalidArg(field, field+" must be an IPv6 CIDR (e.g. 2001:db8::/64)")
	}
	return nil
}

// validateCIDRPrefix проверяет, что value — валидный CIDR-prefix и host-bits=0.
func validateCIDRPrefix(field, value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return invalidArg(field, field+" must be a valid CIDR (e.g. 10.0.0.0/24)")
	}
	if prefix.Masked() != prefix {
		return invalidArg(field,
			field+" must have zero host-bits (use the network address, e.g. 10.0.0.0/24, not 10.0.0.5/24)")
	}
	return nil
}

// netipPrefix — internal alias для netip.Prefix.
type netipPrefix = netip.Prefix

// parseNetipPrefix парсит CIDR-строку в netip.Prefix.
func parseNetipPrefix(s string) (netipPrefix, error) {
	return netip.ParsePrefix(s)
}

// prefixesOverlap возвращает true если два CIDR-блока пересекаются.
func prefixesOverlap(a, b netipPrefix) bool {
	if a.Addr().Is4() != b.Addr().Is4() {
		return false
	}
	if a.Contains(b.Addr()) || b.Contains(a.Addr()) {
		return true
	}
	return false
}

// checkCIDRDisjoint — sync-проверка, что массив CIDR не содержит пересекающихся.
// fieldPrefix — имя поля для error-сообщений (например "v4_cidr_blocks").
func checkCIDRDisjoint(fieldPrefix string, cidrs []string) error {
	prefixes := make([]netipPrefix, 0, len(cidrs))
	for i, c := range cidrs {
		pr, err := parseNetipPrefix(c)
		if err != nil {
			return invalidArg(fmt.Sprintf("%s[%d]", fieldPrefix, i), "must be valid CIDR")
		}
		prefixes = append(prefixes, pr)
	}
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if prefixesOverlap(prefixes[i], prefixes[j]) {
				return status.Errorf(codes.FailedPrecondition, "Subnet CIDRs can not overlap")
			}
		}
	}
	return nil
}

// appendDedup добавляет элементы src в dst, пропуская уже присутствующие в dst.
func appendDedup(dst, src []string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, v := range src {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		dst = append(dst, v)
	}
	return dst
}

// subtractCIDRs возвращает existing без блоков из remove + сколько блоков было
// фактически удалено (для проверки "блок не найден" — mirror v4-поведения).
func subtractCIDRs(existing, remove []string) ([]string, int) {
	toRemove := make(map[string]struct{}, len(remove))
	for _, c := range remove {
		toRemove[c] = struct{}{}
	}
	var remaining []string
	var removed int
	for _, e := range existing {
		if _, ok := toRemove[e]; ok {
			removed++
			continue
		}
		remaining = append(remaining, e)
	}
	return remaining, removed
}

// validateDhcpOptions — verbatim YC contract:
//   - domainName: RFC 1123 DNS name либо empty.
//   - domainNameServers[]: каждый элемент — IP-адрес.
//   - ntpServers[]: каждый элемент — IP-адрес.
func validateDhcpOptions(d *domain.DhcpOptions) error {
	if d == nil {
		return nil
	}
	if err := corevalidate.DhcpDomainName("dhcp_options.domain_name", d.DomainName); err != nil {
		return err
	}
	for _, ns := range d.DomainNameServers {
		if err := corevalidate.IPAddress("dhcp_options.domain_name_servers", ns); err != nil {
			return err
		}
	}
	for _, ntp := range d.NtpServers {
		if err := corevalidate.IPAddress("dhcp_options.ntp_servers", ntp); err != nil {
			return err
		}
	}
	return nil
}

// validateZoneID — sync-валидация zone_id: required + existence в БД.
//
// Возвращает gRPC InvalidArgument с FieldViolation для пустого значения; для
// несуществующей зоны — flat-message `unknown zone id '<zoneId>'` (verbatim YC,
// probe 2026-05-11; kacho-vpc#8). Любая другая ошибка БД → mapRepoErr.
//
// `zr == nil` — безопасный fallback для тестов без zoneReg (skip existence check).
func validateZoneID(ctx context.Context, zr ZoneRegistry, field, zoneID string) error {
	if err := corevalidate.ZoneId(field, zoneID); err != nil {
		return err
	}
	if zr == nil {
		return nil
	}
	_, err := zr.Get(ctx, zoneID)
	if err == nil {
		return nil
	}
	if errors.Is(err, repo.ErrNotFound) {
		return status.Errorf(codes.InvalidArgument, "unknown zone id '%s'", zoneID)
	}
	return mapRepoErr(err)
}
