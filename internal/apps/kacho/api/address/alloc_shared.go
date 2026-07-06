// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

// alloc_shared.go — единый источник IPAM-selection-логики, общий для двух путей
// аллокации: inline create-time (CreateAddressUseCase.doCreate, `create.go`) и
// internal Allocate RPC (AllocateUseCase, `allocate.go`). Раньше двухфазный
// (random-pick + deterministic sweep) v4-цикл и v6-цикл, а также external
// freelist-pop + error-mapping были скопированы байт-в-байт между обоими
// use-case'ами (~4 семейства). Дрейф-риск: фикс алгоритма в одном месте молча
// расходился со вторым (project-rule #11 / evgeniy cohesion).
//
// Каждая функция принимает УЖЕ открытый writer-TX и УЖЕ прочитанный
// *AddressRecord; вызывающий отвечает за pre-checks (nil-spec / idempotent
// already-allocated / empty subnet_id|pool) и за terminal-wrap результата
// (create → allocResult; allocate → finishAllocate). Тексты статусов
// (FailedPrecondition / ResourceExhausted) и slog-сообщения — часть контракта,
// сохранены дословно.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// allocateInternalV4IntoTx — двухфазный (random-pick → deterministic sweep)
// подбор свободного IPv4 по всем subnet.V4CidrBlocks с атомарным claim'ом
// каждого кандидата через SetIPSpec в открытой writer-TX. На успех — updated
// record с проставленным internal_ipv4.address; иначе — gRPC status
// (FailedPrecondition при отсутствии v4-CIDR, ResourceExhausted при исчерпании).
//
// Pre-conditions (проверяет caller): addr.InternalIpv4 != nil, .Address == "",
// .SubnetID != "".
func allocateInternalV4IntoTx(ctx context.Context, w Writer, subnetReader SubnetReader, addr *kachorepo.AddressRecord) (*kachorepo.AddressRecord, error) {
	sub, err := subnetReader.Get(ctx, addr.InternalIpv4.SubnetID)
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
			ip, err := domain.PickRandomIPv4(cidr)
			if err != nil {
				break
			}
			if _, dup := tried[ip]; dup {
				continue
			}
			tried[ip] = struct{}{}
			addr.InternalIpv4.Address = ip
			updated, err := w.Addresses().SetIPSpec(ctx, addr.ID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error",
					"subnet_id", sub.ID, "address_id", addr.ID, "ip_attempt", ip, "err", err)
				return nil, err
			}
			return updated, nil
		}
		// Phase 2: deterministic sweep.
		for _, candidate := range domain.UsableIPv4Sweep(cidr, allocateMaxAttempts-allocateRandomPhase) {
			if _, dup := tried[candidate]; dup {
				continue
			}
			tried[candidate] = struct{}{}
			addr.InternalIpv4.Address = candidate
			updated, err := w.Addresses().SetIPSpec(ctx, addr.ID, nil, addr.InternalIpv4)
			if err != nil {
				if isUniqueViolation(err) {
					totalConflicts++
					addr.InternalIpv4.Address = ""
					continue
				}
				slog.ErrorContext(ctx, "allocator: SetIPSpec returned non-conflict error in sweep",
					"subnet_id", sub.ID, "address_id", addr.ID, "ip_attempt", candidate, "err", err)
				return nil, err
			}
			return updated, nil
		}
	}
	slog.WarnContext(ctx, "allocator: subnet exhausted",
		"subnet_id", sub.ID,
		"address_id", addr.ID,
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

// allocateInternalV6IntoTx — random-pick подбор свободного IPv6 в
// subnet.V6CidrBlocks[0] с атомарным claim'ом через SetInternalIPv6 в открытой
// writer-TX. На успех — updated record с internal_ipv6.address.
//
// Pre-conditions (проверяет caller): addr.InternalIpv6 != nil, .Address == "",
// .SubnetID != "".
func allocateInternalV6IntoTx(ctx context.Context, w Writer, subnetReader SubnetReader, addr *kachorepo.AddressRecord) (*kachorepo.AddressRecord, error) {
	sub, err := subnetReader.Get(ctx, addr.InternalIpv6.SubnetID)
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
		ip, perr := domain.PickRandomIPv6(prefix)
		if perr != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "subnet %s: cannot pick IPv6 in %s: %v", sub.ID, prefix, perr)
		}
		if _, dup := tried[ip]; dup {
			continue
		}
		tried[ip] = struct{}{}
		addr.InternalIpv6.Address = ip
		updated, uerr := w.Addresses().SetInternalIPv6(ctx, addr.ID, addr.InternalIpv6)
		if uerr != nil {
			if isUniqueViolation(uerr) {
				conflicts++
				addr.InternalIpv6.Address = ""
				continue
			}
			slog.ErrorContext(ctx, "v6 allocator: SetInternalIPv6 returned non-conflict error",
				"subnet_id", sub.ID, "address_id", addr.ID, "ip_attempt", ip, "err", uerr)
			return nil, uerr
		}
		return updated, nil
	}
	slog.WarnContext(ctx, "v6 allocator: exhausted attempts",
		"subnet_id", sub.ID, "address_id", addr.ID, "cidr", prefix.String(), "conflicts", conflicts)
	return nil, status.Errorf(codes.ResourceExhausted,
		"subnet %s: could not allocate a free IPv6 in %s after %d attempts (%d unique-conflicts)",
		sub.ID, prefix, v6AllocateMaxAttempts, conflicts)
}

// allocateExternalV4IntoTx — pop next-free IPv4 из freelist пула
// (address_pool_free_ips) в открытой writer-TX + маппинг repo-ошибок в gRPC
// status. Возвращает выделенный IP.
//
// Pre-conditions (проверяет caller): pool резолвлен, len(pool.V4CIDRBlocks) > 0,
// address ещё не имеет external_ipv4.
func allocateExternalV4IntoTx(ctx context.Context, w Writer, poolID, addressID string) (string, error) {
	ip, err := w.Addresses().AllocateIPFromFreelist(ctx, poolID, addressID)
	if err != nil {
		if errors.Is(err, repo.ErrPoolExhausted) {
			return "", status.Errorf(codes.FailedPrecondition,
				"address pool %s exhausted", poolID)
		}
		slog.ErrorContext(ctx, "allocator: AllocateIPFromFreelist failed",
			"pool_id", poolID, "address_id", addressID, "err", err)
		return "", serviceerr.MapRepoErr(fmt.Errorf("%w: allocate from freelist: %v", repo.ErrInternal, err))
	}
	return ip, nil
}

// allocateExternalV6IntoTx — sparse-counter external-IPv6 allocate в открытой
// writer-TX + маппинг repo-ошибок. Возвращает выделенный IP.
//
// Pre-conditions (проверяет caller): pool резолвлен, len(pool.V6CIDRBlocks) > 0,
// address ещё не имеет external_ipv6.
func allocateExternalV6IntoTx(ctx context.Context, w Writer, poolID, addressID, zoneID string) (string, error) {
	ip, err := w.Addresses().AllocateExternalIPv6(ctx, poolID, addressID, zoneID)
	if err != nil {
		if errors.Is(err, repo.ErrPoolExhausted) {
			return "", status.Errorf(codes.FailedPrecondition,
				"address pool %s exhausted (ipv6)", poolID)
		}
		if errors.Is(err, repo.ErrFailedPrecondition) {
			return "", status.Errorf(codes.FailedPrecondition,
				"%s", strings.TrimPrefix(err.Error(), repo.ErrFailedPrecondition.Error()+": "))
		}
		slog.ErrorContext(ctx, "allocator: AllocateExternalIPv6 failed",
			"pool_id", poolID, "address_id", addressID, "err", err)
		return "", serviceerr.MapRepoErr(fmt.Errorf("%w: allocate external ipv6: %v", repo.ErrInternal, err))
	}
	return ip, nil
}
