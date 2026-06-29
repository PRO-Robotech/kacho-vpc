// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"encoding/binary"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// Reserved-срезы анонс-пространства (RFC 6598 Shared Address Space для IPv4,
// provider-ULA для IPv6) и размеры платформенно-назначаемых блоков.
const (
	reservedV4   = "100.64.0.0/10"
	reservedV6   = "fd00:ca00::/48"
	assignV4Bits = 22
	assignV6Bits = 64
)

// validateCIDRBlocks проверяет набор cidr_blocks пула относительно ip_version:
// каждый блок parseable + host-bits=0 (иначе "Illegal argument cidr_blocks"),
// family блока совпадает с ip_version (иначе "...does not match ip_version"),
// блок внутри reserved-среза своей family (иначе "...within the reserved ...
// range"), within-pool блоки не пересекаются. Тексты — часть контракта.
func validateCIDRBlocks(blocks []string, ipVersion domain.IpVersion) error {
	prefixes := make([]netip.Prefix, 0, len(blocks))
	for _, raw := range blocks {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || p.Masked() != p {
			return status.Error(codes.InvalidArgument, "Illegal argument cidr_blocks")
		}
		blockV6 := p.Addr().Is6() && !p.Addr().Is4In6()
		if (ipVersion == domain.IpVersionIPv6) != blockV6 {
			return status.Error(codes.InvalidArgument, "Illegal argument cidr_blocks: does not match ip_version")
		}
		reserved := reservedV4
		if blockV6 {
			reserved = reservedV6
		}
		if !netip.MustParsePrefix(reserved).Overlaps(p) || p.Bits() < netip.MustParsePrefix(reserved).Bits() {
			return status.Errorf(codes.InvalidArgument,
				"Illegal argument cidr_blocks: anycast address pool CIDR must be within the reserved %s range", reserved)
		}
		prefixes = append(prefixes, p)
	}
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if prefixes[i].Overlaps(prefixes[j]) {
				return status.Error(codes.InvalidArgument, "Illegal argument cidr_blocks: blocks must not overlap")
			}
		}
	}
	return nil
}

// assignBlock детерминированно выбирает свободный блок из reserved-среза
// (IPv4 /22, IPv6 /64), не пересекающий ни один из уже выделенных блоков
// (глобально — чтобы auto-пулы не конфликтовали). Сканирование по возрастанию
// индекса субсети — детерминированно. Возвращает один блок.
func assignBlock(ipVersion domain.IpVersion, allocated []string) (string, bool) {
	taken := make([]netip.Prefix, 0, len(allocated))
	for _, raw := range allocated {
		if p, err := netip.ParsePrefix(strings.TrimSpace(raw)); err == nil {
			taken = append(taken, p)
		}
	}
	if ipVersion == domain.IpVersionIPv6 {
		return pickFreeV6(taken)
	}
	return pickFreeV4(taken)
}

// pickFreeV4 — первый свободный /22 в 100.64.0.0/10.
func pickFreeV4(taken []netip.Prefix) (string, bool) {
	reserved := netip.MustParsePrefix(reservedV4)
	baseBytes := reserved.Masked().Addr().As4()
	base := binary.BigEndian.Uint32(baseBytes[:])
	count := 1 << (assignV4Bits - reserved.Bits()) // число /22 в /10
	step := uint32(1) << (32 - assignV4Bits)
	for i := 0; i < count; i++ {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], base+uint32(i)*step)
		cand := netip.PrefixFrom(netip.AddrFrom4(b), assignV4Bits)
		if !overlapsAny(cand, taken) {
			return cand.String(), true
		}
	}
	return "", false
}

// pickFreeV6 — первый свободный /64 в provider-ULA fd00:ca00::/48 (индекс
// субсети — 16 бит в байтах 6-7).
func pickFreeV6(taken []netip.Prefix) (string, bool) {
	reserved := netip.MustParsePrefix(reservedV6)
	base := reserved.Masked().Addr().As16()
	count := 1 << (assignV6Bits - reserved.Bits()) // число /64 в /48
	for i := 0; i < count; i++ {
		b := base
		b[6] = byte(i >> 8)
		b[7] = byte(i)
		cand := netip.PrefixFrom(netip.AddrFrom16(b), assignV6Bits)
		if !overlapsAny(cand, taken) {
			return cand.String(), true
		}
	}
	return "", false
}

// overlapsAny — true если cand пересекается с любым из taken (разные family —
// netip.Prefix.Overlaps возвращает false, поэтому фильтрация по family не нужна).
func overlapsAny(cand netip.Prefix, taken []netip.Prefix) bool {
	for _, t := range taken {
		if cand.Overlaps(t) {
			return true
		}
	}
	return false
}

// toProto — AnycastAddressPoolRecord → *vpcv1.AnycastAddressPool. Локальный
// inline-помощник: единственный consumer проекции — handler ниже. CreatedAt —
// DB-managed timestamp; scope/ip_version/status конвертируются из domain-enum.
func toProto(rec *kachorepo.AnycastAddressPoolRecord) *vpcv1.AnycastAddressPool {
	if rec == nil {
		return nil
	}
	return &vpcv1.AnycastAddressPool{
		Id:          rec.ID,
		ProjectId:   rec.ProjectID,
		CreatedAt:   timestamppb.New(rec.CreatedAt),
		Name:        string(rec.Name),
		Description: string(rec.Description),
		Labels:      domain.LabelsToMap(rec.Labels),
		Scope:       scopeToProto(rec.Scope),
		IpVersion:   ipVersionToProto(rec.IPVersion),
		CidrBlocks:  rec.CIDRBlocks,
		IsDefault:   rec.IsDefault,
		Status:      statusToProto(rec.Status),
	}
}

func scopeToProto(s domain.AnycastScope) vpcv1.AnycastAddressPool_AnycastScope {
	if s == domain.AnycastScopeInternal {
		return vpcv1.AnycastAddressPool_INTERNAL
	}
	return vpcv1.AnycastAddressPool_ANYCAST_SCOPE_UNSPECIFIED
}

func ipVersionToProto(v domain.IpVersion) vpcv1.Address_IpVersion {
	switch v {
	case domain.IpVersionIPv4:
		return vpcv1.Address_IPV4
	case domain.IpVersionIPv6:
		return vpcv1.Address_IPV6
	default:
		return vpcv1.Address_IP_VERSION_UNSPECIFIED
	}
}

func statusToProto(s domain.AnycastPoolStatus) vpcv1.AnycastAddressPool_Status {
	switch s {
	case domain.AnycastPoolStatusCreating:
		return vpcv1.AnycastAddressPool_CREATING
	case domain.AnycastPoolStatusActive:
		return vpcv1.AnycastAddressPool_ACTIVE
	case domain.AnycastPoolStatusDeleting:
		return vpcv1.AnycastAddressPool_DELETING
	default:
		return vpcv1.AnycastAddressPool_STATUS_UNSPECIFIED
	}
}

// scopeFromProto / ipVersionFromProto — proto-enum → domain-enum (handler-вход).
// Proto-enum AnycastScope в фазе 1 несёт только UNSPECIFIED/INTERNAL (EXTERNAL —
// фаза 2). External-валидация живёт в use-case на случай будущего расширения.
func scopeFromProto(s vpcv1.AnycastAddressPool_AnycastScope) domain.AnycastScope {
	if s == vpcv1.AnycastAddressPool_INTERNAL {
		return domain.AnycastScopeInternal
	}
	return domain.AnycastScopeUnspecified
}

func ipVersionFromProto(v vpcv1.Address_IpVersion) domain.IpVersion {
	switch v {
	case vpcv1.Address_IPV4:
		return domain.IpVersionIPv4
	case vpcv1.Address_IPV6:
		return domain.IpVersionIPv6
	default:
		return domain.IpVersionUnspecified
	}
}
