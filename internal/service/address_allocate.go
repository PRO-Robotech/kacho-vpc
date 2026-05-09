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

const allocateMaxAttempts = 32

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

	// Linear sweep по cidr_blocks: пробуем каждый CIDR последовательно.
	for _, cidrStr := range pool.CIDRBlocks {
		cidr, err := netip.ParsePrefix(strings.TrimSpace(cidrStr))
		if err != nil {
			continue
		}
		for attempt := 0; attempt < allocateMaxAttempts; attempt++ {
			ip, err := pickRandomIPv4(cidr)
			if err != nil {
				break // CIDR too small; try next
			}
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
	}
	return nil, status.Errorf(codes.ResourceExhausted,
		"address pool %s exhausted (no free IP in any cidr_block)", pool.ID)
}

// pickRandomIPv4 выбирает random IP из CIDR, исключая network/broadcast addresses
// (для prefix length < 31). Использует crypto/rand для unpredictable allocation.
func pickRandomIPv4(cidr netip.Prefix) (string, error) {
	if !cidr.Addr().Is4() {
		return "", ErrInvalidIPv4
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	if hostBits == 0 {
		// /32 — единственный адрес.
		return cidr.Addr().String(), nil
	}
	maxHosts := uint32(1<<hostBits) - 2 // .0 / .last исключаем
	if maxHosts == 0 {
		// /31 — точка-точка, оба адреса валидны.
		maxHosts = 2
	}
	var randBytes [4]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return "", err
	}
	offset := binary.BigEndian.Uint32(randBytes[:])%maxHosts + 1
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
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
