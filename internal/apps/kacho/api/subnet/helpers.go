// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	// Blank-import регистрирует трансферы Subnet/Address/time через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// marshalSubnetRecord конвертирует repo-entity Subnet в *anypb.Any через
// DTO-реестр. Worker'ы Create/Update/AddCidrBlocks/RemoveCidrBlocks кладут этим
// результат в Operation.response.
func marshalSubnetRecord(rec *kachorepo.SubnetRecord) (*anypb.Any, error) {
	var dst *vpcv1.Subnet
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Subnet: %w", err)
	}
	return anypb.New(dst)
}

// ---- CIDR helpers ----

// validateSubnetV4CIDR — host-bits=0 (canonical form) + ограничение размера /≤28.
// Префикс /29..32 → InvalidArgument "Illegal argument Invalid network prefix /<N>".
func validateSubnetV4CIDR(field, value string) error {
	if err := validateCIDRPrefix(field, value); err != nil {
		return err
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return serviceerr.InvalidArg(field, field+" must be a valid CIDR (e.g. 10.0.0.0/24)")
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
		return serviceerr.InvalidArg(field, field+" must be a valid IPv6 CIDR (e.g. 2001:db8::/64)")
	}
	if !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
		return serviceerr.InvalidArg(field, field+" must be an IPv6 CIDR (e.g. 2001:db8::/64)")
	}
	return nil
}

// validateCIDRPrefix проверяет, что value — валидный CIDR-prefix и host-bits=0.
func validateCIDRPrefix(field, value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return serviceerr.InvalidArg(field, field+" must be a valid CIDR (e.g. 10.0.0.0/24)")
	}
	if prefix.Masked() != prefix {
		// Подсказываем точный masked-адрес сети той же family (v4 → напр.
		// 10.0.0.0/24, v6 → напр. 2001:db8::/64), а не жестко зашитый v4-пример.
		return serviceerr.InvalidArg(field,
			field+" must have zero host-bits (use the network address "+prefix.Masked().String()+")")
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
			return serviceerr.InvalidArg(fmt.Sprintf("%s[%d]", fieldPrefix, i), "must be valid CIDR")
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
// фактически удалено (для проверки "блок не найден").
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

// validateDhcpOptions — валидация DHCP-опций:
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

// validateZoneID — sync-валидация zone_id: required + existence у владельца.
//
// Возвращает gRPC InvalidArgument с FieldViolation для пустого значения; для
// несуществующей зоны — flat-message `unknown zone id '<zoneId>'`. Любая другая
// ошибка → mapRepoErr.
//
// `zr == nil` — безопасный fallback для тестов без zoneReg (existence не проверяем).
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
	return serviceerr.MapRepoErr(err)
}

// validateRegionID — sync-валидация region_id REGIONAL-подсети: required +
// existence у owner-домена Geography (kacho-geo). Зеркало validateZoneID.
//
// Пустое значение → InvalidArgument `region_id is required`; несуществующий
// регион → `unknown region id '<regionId>'`; geo недоступен → пробрасывается
// (Unavailable, fail-closed на мутации). `rr == nil` — fallback для тестов без
// regionReg (existence не проверяем).
func validateRegionID(ctx context.Context, rr RegionRegistry, field, regionID string) error {
	if regionID == "" {
		return serviceerr.InvalidArg(field, field+" is required")
	}
	if rr == nil {
		return nil
	}
	_, err := rr.Get(ctx, regionID)
	if err == nil {
		return nil
	}
	if errors.Is(err, repo.ErrNotFound) {
		return status.Errorf(codes.InvalidArgument, "unknown region id '%s'", regionID)
	}
	return serviceerr.MapRepoErr(err)
}

// validatePlacement — дискриминатор размещения подсети + согласованность пары
// zone_id/region_id. placement_type обязателен (UNSPECIFIED → InvalidArgument).
//
//   - ZONAL    — zone_id required + existence (geo); region_id должен быть пуст.
//   - REGIONAL — region_id required + existence (geo); zone_id должен быть пуст.
//
// Та же форма дублируется DB-CHECK subnets_placement_payload_chk (backstop).
func validatePlacement(ctx context.Context, zr ZoneRegistry, rr RegionRegistry, s domain.Subnet) error {
	switch s.PlacementType {
	case domain.PlacementZonal:
		if s.RegionID != "" {
			return serviceerr.InvalidArg("region_id", "region_id must be empty for ZONAL placement")
		}
		return validateZoneID(ctx, zr, "zone_id", s.ZoneID)
	case domain.PlacementRegional:
		if s.ZoneID != "" {
			return serviceerr.InvalidArg("zone_id", "zone_id must be empty for REGIONAL placement")
		}
		return validateRegionID(ctx, rr, "region_id", s.RegionID)
	default:
		return serviceerr.InvalidArg("placement_type", "placement_type is required (ZONAL or REGIONAL)")
	}
}
