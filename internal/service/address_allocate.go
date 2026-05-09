// Package service — Allocate-RPCs для internal-controller use-case.
//
// Эти методы добавляются к AddressService (см. address.go) через embedding —
// чтобы не разрастать единый struct, allocator-функционал инкапсулирован в
// отдельном AddressAllocator type, который держит ссылки на нужные dependencies.
//
// Атомарность: каждый Allocate работает в одной Postgres-tx (через
// AddressRepo.Insert/Update + UNIQUE constraints миграций 0014/0015).
// Retry на UNIQUE violation — внутри сервиса с bounded attempts.
package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AddressAllocator — internal IPAM движок для AllocateInternalIP / AllocateExternalIP.
//
// Stateless: вся state — в БД (addresses, address_pools, bindings).
type AddressAllocator struct {
	addrRepo   AddressRepo
	subnetRepo SubnetRepo
	pools      *AddressPoolService
}

func NewAddressAllocator(addr AddressRepo, sub SubnetRepo, pools *AddressPoolService) *AddressAllocator {
	return &AddressAllocator{addrRepo: addr, subnetRepo: sub, pools: pools}
}

// AllocateResult — результат allocate-операций.
type AllocateResult struct {
	IP               string
	PoolID           string // только для external; "" для internal
	AlreadyAllocated bool
}

const (
	// allocateMaxAttempts — максимум попыток random-pick + retry-on-conflict.
	// При near-full CIDR (≥95% занято) random-pick имеет high false-fail rate
	// (см. allocateRandomPhase ниже). После этого порога переключаемся на
	// deterministic sweep.
	allocateMaxAttempts = 32

	// allocateRandomPhase — сколько попыток сделать random-pick'ом до того
	// как переключиться на deterministic sweep по тем же CIDR. Random в первые
	// N попыток дешевле (1 SQL/попытка), при low/medium occupancy сходится
	// быстро. Переход в sweep гарантирует closure под high-occupancy.
	allocateRandomPhase = 8
)

// AllocateInternalIP — выделяет next-free IPv4 в subnet, который указан
// в address.internal_ipv4.subnet_id. Idempotent: если IP уже выделен —
// возвращает existing с AlreadyAllocated=true.
func (a *AddressAllocator) AllocateInternalIP(ctx context.Context, addressID string) (*AllocateResult, error) {
	addr, err := a.addrRepo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.InternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no internal_ipv4 spec", addressID)
	}
	if addr.InternalIpv4.Address != "" {
		return &AllocateResult{IP: addr.InternalIpv4.Address, AlreadyAllocated: true}, nil
	}
	if addr.InternalIpv4.SubnetID == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s internal_ipv4.subnet_id is empty", addressID)
	}
	sub, err := a.subnetRepo.Get(ctx, addr.InternalIpv4.SubnetID)
	if err != nil {
		return nil, err
	}
	if len(sub.V4CidrBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"subnet %s has no v4_cidr_blocks", sub.ID)
	}
	cidr, err := netip.ParsePrefix(sub.V4CidrBlocks[0])
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"subnet %s invalid cidr in DB: %v", sub.ID, err)
	}

	for attempt := 0; attempt < allocateMaxAttempts; attempt++ {
		ip, err := pickRandomIPv4(cidr)
		if err != nil {
			return nil, status.Errorf(codes.ResourceExhausted, "subnet too small for allocation")
		}
		addr.InternalIpv4.Address = ip
		updated, err := a.addrRepo.SetIPSpec(ctx, addressID, nil, addr.InternalIpv4)
		if err != nil {
			if isUniqueViolation(err) {
				addr.InternalIpv4.Address = "" // reset for next attempt
				continue
			}
			return nil, err
		}
		return &AllocateResult{IP: updated.InternalIpv4.Address}, nil
	}
	return nil, status.Errorf(codes.ResourceExhausted,
		"subnet %s exhausted (failed to find free IP after %d attempts)", sub.ID, allocateMaxAttempts)
}

// AllocateExternalIP — резолвит pool через cascade и выделяет next-free IPv4
// из его cidr_blocks. Idempotent.
func (a *AddressAllocator) AllocateExternalIP(ctx context.Context, addressID string) (*AllocateResult, error) {
	addr, err := a.addrRepo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.ExternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no external_ipv4 spec", addressID)
	}
	if addr.ExternalIpv4.Address != "" {
		return &AllocateResult{
			IP:               addr.ExternalIpv4.Address,
			PoolID:           addr.ExternalIpv4.AddressPoolID,
			AlreadyAllocated: true,
		}, nil
	}

	// ResolvePoolForAddressObj переиспользует уже-полученный addr — устраняет
	// double-Get в request-path (cascade resolve внутри иначе делает повторный
	// addrRepo.Get для того же id).
	resolved, err := a.pools.ResolvePoolForAddressObj(ctx, addr)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
	}
	pool := resolved.Pool
	if len(pool.CIDRBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address pool %s has no cidr_blocks", pool.ID)
	}

	// Двухфазный allocate по каждому CIDR:
	//  Phase 1 (allocateRandomPhase попыток) — random-pick. Дёшево, сходится
	//          быстро при low/medium occupancy. Без shared-state между попытками.
	//  Phase 2 (deterministic sweep) — линейный обход CIDR с локальным
	//          tried-set для memoization. Гарантирует closure под high-occupancy
	//          (concurrency P0 #2 closure: устраняет ~9% false-fail на /28
	//          при 95%+ occupancy).
	for _, cidrStr := range pool.CIDRBlocks {
		cidr, err := netip.ParsePrefix(strings.TrimSpace(cidrStr))
		if err != nil {
			continue
		}
		tried := make(map[string]struct{}, allocateMaxAttempts)
		// Phase 1: random pick.
		for attempt := 0; attempt < allocateRandomPhase; attempt++ {
			ip, err := pickRandomIPv4(cidr)
			if err != nil {
				break // CIDR too small; try next
			}
			if _, dup := tried[ip]; dup {
				continue
			}
			tried[ip] = struct{}{}
			addr.ExternalIpv4.Address = ip
			addr.ExternalIpv4.AddressPoolID = pool.ID
			updated, err := a.addrRepo.SetIPSpec(ctx, addressID, addr.ExternalIpv4, nil)
			if err != nil {
				if isUniqueViolation(err) {
					addr.ExternalIpv4.Address = ""
					addr.ExternalIpv4.AddressPoolID = ""
					continue
				}
				return nil, err
			}
			return &AllocateResult{
				IP:     updated.ExternalIpv4.Address,
				PoolID: pool.ID,
			}, nil
		}
		// Phase 2: deterministic sweep — гарантия что мы перепробуем все
		// usable IPs в CIDR (с учётом tried-set).
		for _, candidate := range usableIPv4Sweep(cidr, allocateMaxAttempts-allocateRandomPhase) {
			if _, dup := tried[candidate]; dup {
				continue
			}
			tried[candidate] = struct{}{}
			addr.ExternalIpv4.Address = candidate
			addr.ExternalIpv4.AddressPoolID = pool.ID
			updated, err := a.addrRepo.SetIPSpec(ctx, addressID, addr.ExternalIpv4, nil)
			if err != nil {
				if isUniqueViolation(err) {
					addr.ExternalIpv4.Address = ""
					addr.ExternalIpv4.AddressPoolID = ""
					continue
				}
				return nil, err
			}
			return &AllocateResult{
				IP:     updated.ExternalIpv4.Address,
				PoolID: pool.ID,
			}, nil
		}
	}
	return nil, status.Errorf(codes.ResourceExhausted,
		"address pool %s exhausted (no free IP in any cidr_block)", pool.ID)
}

// usableIPv4Sweep — deterministic enumeration usable IPv4 в CIDR (без
// network/broadcast). Используется в Phase 2 allocator'а для гарантии closure
// когда random-pick не сходится. Cap'ируется maxN чтобы не аллокировать
// миллионы строк для больших CIDR; для /28 (14 IP) maxN=24 достаточно.
func usableIPv4Sweep(cidr netip.Prefix, maxN int) []string {
	if !cidr.Addr().Is4() {
		return nil
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	if hostBits >= 32 {
		return nil
	}
	total := uint32(1) << hostBits
	// Skip network/broadcast для /≤30; для /31 оба usable; для /32 один.
	first := uint32(1)
	last := total - 1
	switch hostBits {
	case 0:
		first, last = 0, 1
	case 1:
		first, last = 0, 2
	}
	if uint32(maxN) < last-first {
		last = first + uint32(maxN)
	}
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	out := make([]string, 0, last-first)
	for i := first; i < last; i++ {
		var ipBytes [4]byte
		binary.BigEndian.PutUint32(ipBytes[:], baseInt+i)
		out = append(out, net.IP(ipBytes[:]).String())
	}
	return out
}

// pickRandomIPv4 выбирает random IP из CIDR, исключая network/broadcast addresses
// (для prefix length < 31). Использует crypto/rand для unpredictable allocation.
//
// Edge cases (R8 fix /31 off-by-one):
//   - /32 (hostBits=0): единственный адрес — base.
//   - /31 (hostBits=1): оба адреса валидны (point-to-point) — base+0 или base+1.
//     Раньше offset считался как `rand%2 + 1` → возвращал base+1 или base+2,
//     второй вариант ВЫХОДИЛ за CIDR (UNIQUE-constraint в БД не валидирует
//     CIDR-membership → IP реально аллокировался снаружи pool.cidr).
//   - /≤30 (hostBits≥2): пропускаем .0 (network) и .last (broadcast) →
//     offset в [1, maxHosts].
func pickRandomIPv4(cidr netip.Prefix) (string, error) {
	if !cidr.Addr().Is4() {
		return "", ErrInvalidIPv4
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	var offset uint32
	switch hostBits {
	case 0:
		// /32 — единственный адрес.
		return cidr.Addr().String(), nil
	case 1:
		// /31 — оба валидны: offset ∈ {0, 1}.
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return "", err
		}
		offset = binary.BigEndian.Uint32(randBytes[:]) % 2
	default:
		// /≤30 — пропускаем network/broadcast: offset ∈ [1, 2^hostBits - 2].
		maxHosts := uint32(1<<hostBits) - 2
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return "", err
		}
		offset = binary.BigEndian.Uint32(randBytes[:])%maxHosts + 1
	}
	var ipBytes [4]byte
	binary.BigEndian.PutUint32(ipBytes[:], baseInt+offset)
	return net.IP(ipBytes[:]).String(), nil
}

// isUniqueViolation распознаёт UNIQUE-violation для retry-loop в allocate.
//
// Принципиальный путь: repo через wrapPgErr оборачивает SQLSTATE 23505 в
// ErrAlreadyExists — это и есть contract repo↔service. Substring-fallback
// оставлен для случаев когда какой-то новый repo может вернуть raw pgErr
// без обёртки (defensive). Constraint-specific имена удалены — service не
// должен знать DB-schema.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrAlreadyExists) {
		return true
	}
	// Defensive fallback: общие признаки UNIQUE-violation без leak'а
	// constraint-имён в service-layer.
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") ||
		strings.Contains(msg, "duplicate key value")
}
