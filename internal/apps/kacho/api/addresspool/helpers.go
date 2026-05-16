package addresspool

import (
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressFamily — IP-семейство для cascade-resolve фильтрации (KAC-63).
// Без явного family pool-cascade выбрал бы default v4-пул и для v6-запроса,
// что приводило к Internal "pool has no IPv6 cidr_blocks" в allocator'е.
type AddressFamily int

const (
	FamilyV4 AddressFamily = iota
	FamilyV6
)

// ResolvedPool — результат cascade-резолва, с указанием через какой шаг матчилось.
type ResolvedPool struct {
	Pool            *domain.AddressPool
	MatchedVia      string            // "address_override" | "network_default" | "label_selector" | "zone_default" | "global_default"
	MatchedSelector map[string]string // populated only for label_selector
}

// familyStrict — режим проверки family у CIDR-блока в split-shape (KAC-71).
type familyStrict int

const (
	familyV4Strict familyStrict = iota
	familyV6Strict
)

// validateAddressPoolCIDRs — REQ-IPL-CR-05 / REQ-IPL-CR-06: каждый блок в
// соответствующем слоте обязан быть нужной family + host-bits=0. Сообщения —
// verbatim из acceptance §0:
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

// poolHasFamily — true если pool имеет хотя бы один CIDR-блок запрошенной family.
//
// KAC-71: после split CIDR-блоков family-фильтр становится тривиальным
// `len(V4CIDRBlocks)>0` / `len(V6CIDRBlocks)>0` — без runtime-парсинга. Service-
// слой обеспечивает family-correctness на Create/Update (REQ-IPL-CR-05 / B6 +
// REQ-IPL-UPD-01/02), поэтому колонка является source-of-truth по family.
// Cascade `doResolve` использует это на всех 5 шагах единообразно — pool без
// требуемой family пропускается, cascade проваливается дальше (REQ-RESOLVE-06,
// REQ-RESOLVE-07).
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
