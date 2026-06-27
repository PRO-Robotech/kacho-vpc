// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"errors"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// appendNewCIDRs добавляет в existing блоки из add, пропуская уже присутствующие
// (дедуп). Возвращает объединенный набор и подмножество реально новых блоков
// (для materialization только дельты freelist). Используется :addCidrBlocks.
func appendNewCIDRs(existing, add []string) (merged, added []string) {
	seen := make(map[string]struct{}, len(existing))
	for _, v := range existing {
		seen[v] = struct{}{}
	}
	merged = append([]string{}, existing...)
	for _, v := range add {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		merged = append(merged, v)
		added = append(added, v)
	}
	return merged, added
}

// subtractCIDRSet возвращает existing без блоков из remove + сколько блоков было
// фактически удалено (для проверки "блок не найден"). Используется :removeCidrBlocks.
func subtractCIDRSet(existing, remove []string) (remaining []string, removed int) {
	toRemove := make(map[string]struct{}, len(remove))
	for _, c := range remove {
		toRemove[c] = struct{}{}
	}
	for _, e := range existing {
		if _, ok := toRemove[e]; ok {
			removed++
			continue
		}
		remaining = append(remaining, e)
	}
	return remaining, removed
}

// AddressFamily — IP-семейство для фильтрации в cascade-resolve.
// Без явного family pool-cascade выбрал бы default v4-пул и для v6-запроса,
// что приводило бы к Internal "pool has no IPv6 cidr_blocks" в allocator'е.
type AddressFamily int

const (
	FamilyV4 AddressFamily = iota
	FamilyV6
)

// ResolvedPool — результат cascade-резолва, с указанием через какой шаг матчилось.
type ResolvedPool struct {
	Pool       *domain.AddressPool
	MatchedVia string // "network_default" | "zone_default" | "global_default"
}

// familyStrict — режим проверки family у CIDR-блока в split-shape (v4/v6).
type familyStrict int

const (
	familyV4Strict familyStrict = iota
	familyV6Strict
)

// validateAddressPoolCIDRs проверяет, что каждый блок в соответствующем слоте —
// нужной family и с host-bits=0. Тексты ошибок — часть контракта:
//   - `v4_cidr_blocks[N]: "..." is not an IPv4 prefix`
//   - `v6_cidr_blocks[N]: "..." is not an IPv6 prefix`
//   - `<field>[N]: "..." host bits must be zero (use ...)` — общая форма для
//     обеих family.
func validateAddressPoolCIDRs(field string, blocks []string, want familyStrict) error {
	for i, c := range blocks {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return status.Errorf(codes.InvalidArgument,
				"%s[%d]: %q is not a valid CIDR prefix: %v", field, i, c, err)
		}
		// Family-фильтр первым — иначе host-bits сообщение будет вводить в
		// заблуждение для cross-family-prefix'а.
		isV6 := p.Addr().Is6() && !p.Addr().Is4In6()
		switch want {
		case familyV4Strict:
			if isV6 {
				return status.Errorf(codes.InvalidArgument,
					"%s[%d]: %q is not an IPv4 prefix", field, i, c)
			}
		case familyV6Strict:
			if !isV6 {
				return status.Errorf(codes.InvalidArgument,
					"%s[%d]: %q is not an IPv6 prefix", field, i, c)
			}
		}
		// Host-bits должны быть 0 (canonical form: 198.51.100.0/24, не /5;
		// для v6 — 2001:db8::/64, не 2001:db8::5/64).
		if p.Masked() != p {
			return status.Errorf(codes.InvalidArgument,
				"%s[%d]: %q host bits must be zero (use %s)",
				field, i, c, p.Masked().String())
		}
	}
	return nil
}

// checkPoolCIDRsDisjoint — sync within-request precheck, что блоки в САМОМ
// запросе попарно не пересекаются (v4 с v4, v6 с v6). Fast-fail до writer-TX →
// InvalidArgument "address pool CIDRs can not overlap". DB EXCLUDE (миграция
// 0004) — backstop для cross-pool/concurrent. Повторяет подход Subnet
// `checkCIDRDisjoint` через net/netip. Блоки уже провалидированы
// validateAddressPoolCIDRs (family + host-bits=0), поэтому Parse не должен
// падать; на всякий случай некорректный prefix пропускаем (поймает EXCLUDE-cast).
func checkPoolCIDRsDisjoint(v4, v6 []string) error {
	if err := pairwiseDisjoint(v4); err != nil {
		return err
	}
	return pairwiseDisjoint(v6)
}

// pairwiseDisjoint проверяет, что ни одна пара префиксов в blocks не пересекается
// (overlapsPrefix симметричен: A ⊇ B ИЛИ B ⊇ A ИЛИ равны).
func pairwiseDisjoint(blocks []string) error {
	prefixes := make([]netip.Prefix, 0, len(blocks))
	for _, c := range blocks {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			continue
		}
		prefixes = append(prefixes, p.Masked())
	}
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if prefixesOverlap(prefixes[i], prefixes[j]) {
				return status.Error(codes.InvalidArgument,
					"address pool CIDRs can not overlap")
			}
		}
	}
	return nil
}

// prefixesOverlap — true если один префикс содержит другой (или они равны).
func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Overlaps(b)
}

// mapCIDROverlap конвертирует ошибку репо в gRPC-status. При пересечении на
// DB-уровне InsertCidrBlocks возвращает serviceerr.ErrFailedPrecondition —
// отдаем FailedPrecondition с текстом "address pool CIDRs can not overlap"
// (handler-passthrough сохранит его). Прочие ошибки — через serviceerr.MapRepoErr.
func mapCIDROverlap(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, serviceerr.ErrFailedPrecondition) {
		return status.Error(codes.FailedPrecondition,
			"address pool CIDRs can not overlap")
	}
	return serviceerr.MapRepoErr(err)
}

// poolHasFamily — true если pool имеет хотя бы один CIDR-блок запрошенной family.
//
// При split CIDR-блоков family-фильтр тривиален: `len(V4CIDRBlocks)>0` /
// `len(V6CIDRBlocks)>0` — без runtime-парсинга. Service-слой обеспечивает
// family-correctness на Create/Update, поэтому колонка — source-of-truth по
// family. Cascade `doResolve` использует это единообразно на всех шагах — pool
// без требуемой family пропускается, cascade проваливается дальше.
func poolHasFamily(pool *domain.AddressPool, family AddressFamily) bool {
	if pool == nil {
		return false
	}
	switch family {
	case FamilyV6:
		return len(pool.V6CIDRBlocks) > 0
	default:
		return len(pool.V4CIDRBlocks) > 0
	}
}

// usableIPv4Count — usable IPs в CIDR (исключая network+broadcast).
// Для /N: 2^(32-N) - 2; для /31: 2 (RFC 3021); для /32: 1.
// Если CIDR невалиден — 0.
func usableIPv4Count(cidr string) int64 {
	p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil || !p.Addr().Is4() {
		return 0
	}
	bits := p.Bits()
	if bits == 32 {
		return 1
	}
	if bits == 31 {
		return 2
	}
	hostBits := 32 - bits
	if hostBits >= 31 {
		return 0
	}
	return int64(1)<<hostBits - 2
}
