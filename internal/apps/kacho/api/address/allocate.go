package address

// Allocate* — internal-only IPAM allocate (вызывается из
// kacho.cloud.vpc.v1.InternalAddressService). Принимает уже-созданный Address,
// резолвит AddressPool по cascade и атомарно проставляет IP в БД.
//
// Idempotent: если у Address уже есть IP — возвращает его без аллокации.
// Если pools == nil — Allocate* недоступны (test-only setup).
//
// Wave 3 (KAC-94): эти методы раньше висели на `*AddressService` (extension
// methods). Сейчас живут в отдельном `AllocateUseCase`, который инжектируется
// в `InternalAddressAllocateHandler` (см. composition root в cmd/vpc/main.go).

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// AllocateUseCase — internal-only IPAM allocate (4 family-варианта).
//
// Результат allocate-операций — `domain.AllocateResult` (вынесен в domain
// leaf, чтобы избежать import-cycle с `internal/handler.AddressAllocator`).
type AllocateUseCase struct {
	repo         AddressRepo
	subnetReader SubnetReader
	pools        PoolService // nil → Allocate*ExternalIP* недоступны
}

// NewAllocateUseCase создаёт AllocateUseCase.
func NewAllocateUseCase(repo AddressRepo, subnetReader SubnetReader, pools PoolService) *AllocateUseCase {
	return &AllocateUseCase{repo: repo, subnetReader: subnetReader, pools: pools}
}

// AllocateInternalIP — выделяет next-free IPv4 в subnet, который указан
// в address.internal_ipv4.subnet_id. Idempotent.
//
// Iterate по ВСЕМ V4CidrBlocks subnet'а: двухфазный allocator (random pick +
// deterministic sweep с tried-set) устраняет false-fail на near-full subnet.
func (u *AllocateUseCase) AllocateInternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	addr, err := u.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.InternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no internal_ipv4 spec", addressID)
	}
	if addr.InternalIpv4.Address != "" {
		return &domain.AllocateResult{IP: addr.InternalIpv4.Address, AlreadyAllocated: true}, nil
	}
	if addr.InternalIpv4.SubnetID == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s internal_ipv4.subnet_id is empty", addressID)
	}
	sub, err := u.subnetReader.Get(ctx, addr.InternalIpv4.SubnetID)
	if err != nil {
		return nil, err
	}
	if len(sub.V4CidrBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"subnet %s has no IPv4 CIDR", sub.ID)
	}

	parsedV4Count := 0
	totalConflicts := 0
	skippedNonV4 := 0
	parseFails := 0
	for _, cidrStr := range sub.V4CidrBlocks {
		cidr, err := netip.ParsePrefix(strings.TrimSpace(cidrStr))
		if err != nil {
			parseFails++
			slog.WarnContext(ctx, "allocator: skipping unparseable subnet cidr",
				"subnet_id", sub.ID, "cidr", cidrStr, "err", err)
			continue
		}
		if !cidr.Addr().Is4() {
			skippedNonV4++
			continue
		}
		parsedV4Count++
		tried := make(map[string]struct{}, allocateMaxAttempts)
		// Phase 1: random pick.
		for attempt := 0; attempt < allocateRandomPhase; attempt++ {
			ip, err := pickRandomIPv4(cidr)
			if err != nil {
				break
			}
			if _, dup := tried[ip]; dup {
				continue
			}
			tried[ip] = struct{}{}
			addr.InternalIpv4.Address = ip
			updated, err := u.repo.SetIPSpec(ctx, addressID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error",
					"subnet_id", sub.ID, "address_id", addressID, "ip_attempt", ip, "err", err)
				return nil, err
			}
			return &domain.AllocateResult{IP: updated.InternalIpv4.Address}, nil
		}
		// Phase 2: deterministic sweep.
		for _, candidate := range usableIPv4Sweep(cidr, allocateMaxAttempts-allocateRandomPhase) {
			if _, dup := tried[candidate]; dup {
				continue
			}
			tried[candidate] = struct{}{}
			addr.InternalIpv4.Address = candidate
			updated, err := u.repo.SetIPSpec(ctx, addressID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error in sweep",
					"subnet_id", sub.ID, "address_id", addressID, "ip_attempt", candidate, "err", err)
				return nil, err
			}
			return &domain.AllocateResult{IP: updated.InternalIpv4.Address}, nil
		}
	}
	slog.WarnContext(ctx, "allocator: subnet exhausted",
		"subnet_id", sub.ID,
		"address_id", addressID,
		"cidr_blocks", sub.V4CidrBlocks,
		"parsed_ipv4", parsedV4Count,
		"skipped_non_v4", skippedNonV4,
		"parse_fails", parseFails,
		"unique_conflicts", totalConflicts)
	if parsedV4Count == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"subnet %s has no IPv4 cidr_blocks (allocator requires IPv4)", sub.ID)
	}
	return nil, status.Errorf(codes.ResourceExhausted,
		"subnet %s exhausted (tried %d random + %d sweep IPs across %d cidr_blocks; %d unique-conflicts)",
		sub.ID, allocateRandomPhase, allocateMaxAttempts-allocateRandomPhase, parsedV4Count, totalConflicts)
}

// AllocateInternalIPv6 — выделяет случайный свободный IPv6 внутри
// subnet.v6_cidr_blocks[0] для Address с заполненным internal_ipv6.subnet_id.
func (u *AllocateUseCase) AllocateInternalIPv6(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	addr, err := u.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.InternalIpv6 == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "address %s has no internal_ipv6 spec", addressID)
	}
	if addr.InternalIpv6.Address != "" {
		return &domain.AllocateResult{IP: addr.InternalIpv6.Address, AlreadyAllocated: true}, nil
	}
	if addr.InternalIpv6.SubnetID == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "address %s internal_ipv6.subnet_id is empty", addressID)
	}
	sub, err := u.subnetReader.Get(ctx, addr.InternalIpv6.SubnetID)
	if err != nil {
		return nil, err
	}
	if len(sub.V6CidrBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "subnet %s has no v6_cidr_blocks", sub.ID)
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(sub.V6CidrBlocks[0]))
	if err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
		return nil, status.Errorf(codes.FailedPrecondition, "subnet %s has invalid v6 cidr block %q", sub.ID, sub.V6CidrBlocks[0])
	}
	tried := make(map[string]struct{}, v6AllocateMaxAttempts)
	conflicts := 0
	for attempt := 0; attempt < v6AllocateMaxAttempts; attempt++ {
		ip, perr := pickRandomIPv6(prefix)
		if perr != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "subnet %s: cannot pick IPv6 in %s: %v", sub.ID, prefix, perr)
		}
		if _, dup := tried[ip]; dup {
			continue
		}
		tried[ip] = struct{}{}
		addr.InternalIpv6.Address = ip
		updated, uerr := u.repo.SetInternalIPv6(ctx, addressID, addr.InternalIpv6)
		if uerr != nil {
			if isUniqueViolation(uerr) {
				conflicts++
				addr.InternalIpv6.Address = ""
				continue
			}
			slog.ErrorContext(ctx, "v6 allocator: SetInternalIPv6 returned non-conflict error",
				"subnet_id", sub.ID, "address_id", addressID, "ip_attempt", ip, "err", uerr)
			return nil, uerr
		}
		return &domain.AllocateResult{IP: updated.InternalIpv6.Address}, nil
	}
	slog.WarnContext(ctx, "v6 allocator: exhausted attempts",
		"subnet_id", sub.ID, "address_id", addressID, "cidr", prefix.String(), "conflicts", conflicts)
	return nil, status.Errorf(codes.ResourceExhausted,
		"subnet %s: could not allocate a free IPv6 in %s after %d attempts (%d unique-conflicts)",
		sub.ID, prefix, v6AllocateMaxAttempts, conflicts)
}

// AllocateExternalIP — резолвит pool через cascade и выделяет next-free IPv4
// из его freelist (address_pool_free_ips, миграция 0014). Idempotent.
//
// PG-native allocator: один SQL-statement (FOR UPDATE SKIP LOCKED → DELETE
// FROM freelist → UPDATE addresses) на каждую попытку. Нулевая contention
// между параллельными аллокаторами; каждая аллокация O(1) по числу IP в pool'е.
func (u *AllocateUseCase) AllocateExternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	addr, err := u.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.ExternalIpv4 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no external_ipv4 spec", addressID)
	}
	if addr.ExternalIpv4.Address != "" {
		return &domain.AllocateResult{
			IP:               addr.ExternalIpv4.Address,
			PoolID:           addr.ExternalIpv4.AddressPoolID,
			AlreadyAllocated: true,
		}, nil
	}
	if u.pools == nil {
		return nil, status.Error(codes.Unavailable, "address pool service not configured")
	}
	resolved, err := u.pools.ResolvePoolForAddressObjFamily(ctx, addr, addresspool.FamilyV4)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
	}
	pool := resolved.Pool
	if len(pool.V4CIDRBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address pool %s has no v4_cidr_blocks", pool.ID)
	}
	ip, err := u.repo.AllocateIPFromFreelist(ctx, pool.ID, addressID)
	if err != nil {
		if errors.Is(err, ports.ErrPoolExhausted) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"address pool %s exhausted", pool.ID)
		}
		slog.ErrorContext(ctx, "allocator: AllocateIPFromFreelist failed",
			"pool_id", pool.ID, "address_id", addressID, "err", err)
		return nil, status.Errorf(codes.Internal, "allocate from freelist: %v", err)
	}
	return &domain.AllocateResult{IP: ip, PoolID: pool.ID}, nil
}

// AllocateExternalIPv6 (KAC-60) — выделяет внешний IPv6 для address через
// sparse counter-based allocator (миграция 0021). Зеркало AllocateExternalIP
// для v4: cascade resolve pool → repo.AllocateExternalIPv6 → IP.
func (u *AllocateUseCase) AllocateExternalIPv6(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	addr, err := u.repo.Get(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if addr.ExternalIpv6 == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address %s has no external_ipv6 spec", addressID)
	}
	if addr.ExternalIpv6.Address != "" {
		return &domain.AllocateResult{
			IP:               addr.ExternalIpv6.Address,
			PoolID:           addr.ExternalIpv6.AddressPoolID,
			AlreadyAllocated: true,
		}, nil
	}
	if u.pools == nil {
		return nil, status.Error(codes.Unavailable, "address pool service not configured")
	}
	resolved, err := u.pools.ResolvePoolForAddressObjFamily(ctx, addr, addresspool.FamilyV6)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve address pool: %v", err)
	}
	pool := resolved.Pool
	if len(pool.V6CIDRBlocks) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"address pool %s has no v6_cidr_blocks", pool.ID)
	}

	ip, err := u.repo.AllocateExternalIPv6(ctx, pool.ID, addressID, addr.ExternalIpv6.ZoneID)
	if err != nil {
		if errors.Is(err, ports.ErrPoolExhausted) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"address pool %s exhausted (ipv6)", pool.ID)
		}
		if errors.Is(err, ports.ErrFailedPrecondition) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"%s", strings.TrimPrefix(err.Error(), ports.ErrFailedPrecondition.Error()+": "))
		}
		slog.ErrorContext(ctx, "allocator: AllocateExternalIPv6 failed",
			"pool_id", pool.ID, "address_id", addressID, "err", err)
		return nil, status.Errorf(codes.Internal, "allocate external ipv6: %v", err)
	}
	return &domain.AllocateResult{IP: ip, PoolID: pool.ID}, nil
}
