// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ResolverService — cascade-resolve движок AddressPool. Используется напрямую
// `apps/kacho/api/address/*` use-case'ами (через port `PoolService` в
// address-pkg iface.go) — Address.Create / Allocate*IP проводят resolve, чтобы
// выяснить, из какого pool брать IP.
//
// Это **не handler-уровневый use-case**, а сервис-инфраструктурный (часть
// pool-доменной бизнес-логики, переиспользуемая Address use-case'ами).
//
// Cascade — чистое read-path. На каждый вызов открывается **одна** read-TX
// `kacho.Repository.Reader(ctx)`, чтобы все шаги cascade видели consistent
// snapshot (read-committed на slave) и не ловили inconsistent view при
// concurrent admin write'ах.
//
// Cascade сведен к network_default → zone_default → global_default (шаги
// override per-address и label-selector per-cloud удалены вместе с их RPC).
type ResolverService struct {
	repo       Repo
	addrRepo   AddressRepo
	subnetRepo SubnetReader
}

// NewResolverService собирает cascade-resolve движок.
func NewResolverService(
	r Repo,
	addrRepo AddressRepo,
	subnetRepo SubnetReader,
) *ResolverService {
	return &ResolverService{
		repo: r, addrRepo: addrRepo, subnetRepo: subnetRepo,
	}
}

// ResolvePoolForAddressObjFamily — cascade-resolve с явным IP-family фильтром.
// Каждый step отвергает pool без CIDR нужной family и проваливается на следующий
// step, чтобы default v4-пул не «утаскивал» v6-аллокацию (и наоборот).
//
// Cascade:
//
//  1. address_pool_network_default  (explicit per-network; для internal IP)
//  2. zone-default                  (is_default=true для zone+kind)
//  3. global-default                (is_default=true для zone IS NULL и kind)
//
// Используется allocate-путями (external v4/v6) и Address.Create pre-resolve.
// Если ни один шаг не дал результата — возвращает ErrPoolNotResolved (caller
// возвращает FailedPrecondition / ResourceExhausted).
func (s *ResolverService) ResolvePoolForAddressObjFamily(ctx context.Context, addr *kachorepo.AddressRecord, family AddressFamily) (*ResolvedPool, error) {
	if addr == nil {
		return nil, status.Error(codes.InvalidArgument, "ResolvePoolForAddressObjFamily: addr is required")
	}
	return s.doResolve(ctx, addr.ID, &addr.Address, family)
}

// doResolve — единая реализация cascade. Если preloadedAddr != nil — переиспользуется
// без дополнительного s.addrRepo.Get (устраняет double-Get в hot path).
//
// family — pool должен иметь хотя бы один CIDR требуемой family.
func (s *ResolverService) doResolve(
	ctx context.Context,
	addressID string,
	preloadedAddr *domain.Address,
	family AddressFamily,
) (*ResolvedPool, error) {

	const kindHint = domain.AddressPoolKindExternalPublic

	rd, err := s.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()

	// Resolve network_id, zone_id из address-spec.
	networkID := ""
	zoneID := ""
	if addressID != "" {
		a := preloadedAddr
		if a == nil {
			fetched, gerr := s.addrRepo.Get(ctx, addressID)
			if gerr != nil {
				return nil, gerr
			}
			a = &fetched.Address
		}
		if a.ExternalIpv4 != nil && a.ExternalIpv4.ZoneID != "" {
			zoneID = a.ExternalIpv4.ZoneID
		}
		if a.ExternalIpv6 != nil && a.ExternalIpv6.ZoneID != "" {
			zoneID = a.ExternalIpv6.ZoneID
		}
		if a.InternalIpv4 != nil && a.InternalIpv4.SubnetID != "" {
			sub, gerr := s.subnetRepo.Get(ctx, a.InternalIpv4.SubnetID)
			if gerr == nil {
				networkID = sub.NetworkID
				if zoneID == "" && sub.ZoneID != "" {
					zoneID = sub.ZoneID
				}
			}
		}
	}

	// Step 1: network_default (только когда есть networkID — internal IP path).
	if networkID != "" {
		if poolID, gerr := rd.AddressPoolBindings().GetNetworkDefault(ctx, networkID); gerr == nil && poolID != "" {
			pool, gerr := rd.AddressPools().Get(ctx, poolID)
			if gerr == nil && poolHasFamilyRec(pool, family) {
				return &ResolvedPool{Pool: &pool.AddressPool, MatchedVia: "network_default"}, nil
			}
		}
	}

	// Step 2: zone_default — точный match по (zone, kind, family).
	if zoneID != "" {
		if pool, gerr := rd.AddressPools().GetDefaultForZone(ctx, zoneID, kindHint); gerr == nil && poolHasFamilyRec(pool, family) {
			return &ResolvedPool{Pool: &pool.AddressPool, MatchedVia: "zone_default"}, nil
		}
	}

	// Step 3: global_default (zone_id IS NULL).
	if pool, gerr := rd.AddressPools().GetDefaultForZone(ctx, "", kindHint); gerr == nil && poolHasFamilyRec(pool, family) {
		return &ResolvedPool{Pool: &pool.AddressPool, MatchedVia: "global_default"}, nil
	}

	return nil, fmt.Errorf("%w for address %s (network %s, family=%d)", ErrPoolNotResolved, addressID, networkID, family)
}

// poolHasFamilyRec — family-фильтр для Record-обертки.
func poolHasFamilyRec(rec *kachorepo.AddressPoolRecord, family AddressFamily) bool {
	if rec == nil {
		return false
	}
	return poolHasFamily(&rec.AddressPool, family)
}
