// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

// Anycast-аллокация через AddressService.Create. Anycast-Address — это обычный
// ресурс Address (id-prefix adr) с anycast-spec: network-scoped host /32 (IPv4) /
// /128 (IPv6), выделенный из AnycastAddressPool, приаттаченного к сети (либо из
// платформенного is_default-пула). Аллокация race-safe на DB-уровне: финальный
// гейт двойной аллокации одного IP — глобально-уникальный индекс
// addresses_anycast_host_uniq (insert-host → catch-unique → retry next host).
//
// Ошибки аллокации/ёмкости — generic ("could not allocate anycast address",
// "anycast address pool is not available in network"): пул не должен стать
// capacity/existence oracle (анти-oracle).

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// AnycastAddrSpec — параметры anycast-аллокации в CreateInput. AnycastPoolID
// пуст → аллокация из платформенного is_default-пула семейства.
type AnycastAddrSpec struct {
	NetworkID     string
	IpVersion     domain.IpVersion
	AnycastPoolID string
}

// anycastMaxCandidates — потолок host-кандидатов на одну аллокацию. Для широких
// блоков (random-sample), для узких (/32,/31,/30 …) — полный детерминированный
// sweep. Исчерпание кандидатов → generic "could not allocate anycast address".
const anycastMaxCandidates = 256

// errAnycastUnavailable — пул недоступен в сети (нет attach и не is_default),
// либо его семейство не совпадает с запросом, либо пул/default отсутствует.
// Текст generic (анти-oracle): не подтверждает существование/ownership пула.
var errAnycastUnavailable = status.Error(codes.FailedPrecondition,
	"anycast address pool is not available in network")

// doCreateAnycast — async-часть Create для anycast-spec. Атомарно в одной
// writer-TX: scope-валидация сети, резолв+availability пула, Insert anycast-
// Address, аллокация глобально-уникального host (retry-on-unique), outbox +
// owner-tuple intent. Любая ошибка → Abort (orphan-окно закрыто).
func (u *CreateAddressUseCase) doCreateAnycast(ctx context.Context, addrID string, in CreateInput) (*anypb.Any, error) {
	spec := in.AnycastSpec

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// Scope-валидация: сеть должна существовать (anycast network-scoped). FK
	// anycast_network_id→networks — DB-backstop, здесь — явная NotFound-проверка.
	if _, nerr := w.Networks().Get(ctx, spec.NetworkID); nerr != nil {
		if errors.Is(nerr, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", spec.NetworkID)
		}
		return nil, serviceerr.MapRepoErr(nerr)
	}

	pool, perr := u.resolveAnycastPool(ctx, w, spec)
	if perr != nil {
		return nil, perr
	}

	a := &domain.Address{
		ID:          addrID,
		ProjectID:   in.ProjectID,
		Name:        domain.RcNameVPC(in.Name),
		Description: domain.RcDescription(in.Description),
		Labels:      domain.LabelsFromMap(in.Labels),
		Type:        domain.AddressTypeInternal,
		IpVersion:   spec.IpVersion,
		// Anycast-аллокация активна сразу (не reserved-черновик).
		Used:               true,
		DeletionProtection: in.DeletionProtection,
		Anycast: &domain.AnycastSpec{
			NetworkID:     spec.NetworkID,
			AnycastPoolID: pool.ID,
		},
	}

	created, err := w.Addresses().Insert(ctx, a)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}

	host, aerr := u.allocateAnycastHost(ctx, w, created.ID, created.Anycast, pool, spec.IpVersion)
	if aerr != nil {
		return nil, aerr
	}
	created.Anycast.Address = host

	if err := w.Outbox().Emit(ctx, "Address", created.ID, "CREATED", helpers.DomainToMap(created)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	items := []fgaregister.Item{
		fgaregister.ProjectHierarchyItem(in.ProjectID, "vpc_address", created.ID,
			domain.LabelsToMap(created.Labels)),
	}
	if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(items...)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if u.registrar != nil {
		if err := u.registrar.Register(ctx, items); err != nil {
			return nil, err
		}
	}
	return marshalAddressRecord(created)
}

// resolveAnycastPool — резолвит пул (explicit anycast_pool_id ИЛИ is_default
// семейства) и проверяет доступность в сети: is_default авто-доступен, иначе
// требуется attach-pivot. Семейство пула обязано совпадать с запрошенным.
// Любое расхождение → generic errAnycastUnavailable (анти-oracle).
func (u *CreateAddressUseCase) resolveAnycastPool(ctx context.Context, w Writer, spec *AnycastAddrSpec) (*kachorepo.AnycastAddressPoolRecord, error) {
	var pool *kachorepo.AnycastAddressPoolRecord
	if spec.AnycastPoolID != "" {
		p, err := w.AnycastAddressPools().Get(ctx, spec.AnycastPoolID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, errAnycastUnavailable
			}
			return nil, serviceerr.MapRepoErr(err)
		}
		pool = p
	} else {
		p, err := w.AnycastAddressPools().DefaultForFamily(ctx, spec.IpVersion)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, errAnycastUnavailable
			}
			return nil, serviceerr.MapRepoErr(err)
		}
		pool = p
	}
	if pool.IPVersion != spec.IpVersion {
		return nil, errAnycastUnavailable
	}
	// is_default — авто-доступен каждой сети без явного attach; остальные — только
	// если приаттачены к этой сети.
	if !pool.IsDefault {
		attached, err := w.AnycastAddressPools().IsAttached(ctx, pool.ID, spec.NetworkID)
		if err != nil {
			return nil, serviceerr.MapRepoErr(err)
		}
		if !attached {
			return nil, errAnycastUnavailable
		}
	}
	return pool, nil
}

// allocateAnycastHost — перебирает host-кандидатов из блоков пула, ставит каждый
// через SetAnycast; глобально-уникальный expression-индекс addresses_anycast_host_uniq
// (anycast->>'address', 23505 → ErrAlreadyExists) отбивает занятый IP, аллокатор
// берёт следующий. Исчерпание → generic
// FailedPrecondition (без exhausted/capacity/N-free — анти-oracle).
func (u *CreateAddressUseCase) allocateAnycastHost(ctx context.Context, w Writer, addressID string, spec *domain.AnycastSpec, pool *kachorepo.AnycastAddressPoolRecord, ipVersion domain.IpVersion) (string, error) {
	for _, block := range pool.CIDRBlocks {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(block))
		if err != nil {
			continue
		}
		switch ipVersion {
		case domain.IpVersionIPv6:
			if !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
				continue
			}
		default:
			if !prefix.Addr().Is4() {
				continue
			}
		}
		candidates, cerr := anycastHostCandidates(prefix, anycastMaxCandidates)
		if cerr != nil {
			continue
		}
		for _, candidate := range candidates {
			spec.Address = candidate
			_, serr := w.Addresses().SetAnycast(ctx, addressID, spec)
			if serr != nil {
				if isUniqueViolation(serr) {
					spec.Address = ""
					continue
				}
				return "", serviceerr.MapRepoErr(serr)
			}
			return candidate, nil
		}
	}
	spec.Address = ""
	return "", status.Error(codes.FailedPrecondition, "could not allocate anycast address")
}

// anycastHostCandidates — host-кандидаты внутри prefix (без network/broadcast):
// узкий блок — полный детерминированный sweep usable-хостов; широкий — random
// sample (crypto/rand) до maxN, чтобы избежать thundering-herd на первых .1/.2
// под конкурентной аллокацией. /32 → единственный host; /31 → один usable.
func anycastHostCandidates(prefix netip.Prefix, maxN int) ([]string, error) {
	if prefix.Addr().Is4() {
		return anycastV4Candidates(prefix, maxN), nil
	}
	return anycastV6Candidates(prefix, maxN)
}

// anycastV4Candidates — usable IPv4-хосты prefix'а как offset'ы от network-адреса.
func anycastV4Candidates(prefix netip.Prefix, maxN int) []string {
	hostBits := 32 - prefix.Bits()
	base := prefix.Masked().Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	var lo, hi uint32
	switch {
	case hostBits == 0: // /32 — единственный host
		lo, hi = 0, 0
	case hostBits == 1: // /31 — один usable (без network-адреса)
		lo, hi = 1, 1
	default: // без .0 (network) и broadcast
		lo = 1
		hi = (uint32(1) << hostBits) - 2
	}
	v4 := func(off uint32) string {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], baseInt+off)
		return net.IP(b[:]).String()
	}
	size := hi - lo + 1
	if size <= uint32(maxN) {
		out := make([]string, 0, size)
		for off := lo; off <= hi; off++ {
			out = append(out, v4(off))
			if off == hi {
				break // защита от uint32-overflow на верхней границе
			}
		}
		return out
	}
	// Широкий блок — случайная выборка maxN уникальных offset'ов.
	span := hi - lo + 1
	tried := make(map[uint32]struct{}, maxN)
	out := make([]string, 0, maxN)
	for len(out) < maxN && len(tried) < int(span) {
		var rnd [4]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			break
		}
		off := lo + binary.BigEndian.Uint32(rnd[:])%span
		if _, dup := tried[off]; dup {
			continue
		}
		tried[off] = struct{}{}
		out = append(out, v4(off))
	}
	return out
}

// anycastV6Candidates — usable IPv6-хосты: /128 → единственный адрес, иначе до
// maxN случайных host'ов (без subnet-router anycast all-zeros).
func anycastV6Candidates(prefix netip.Prefix, maxN int) ([]string, error) {
	if 128-prefix.Bits() == 0 {
		return []string{prefix.Masked().Addr().String()}, nil
	}
	tried := make(map[string]struct{}, maxN)
	out := make([]string, 0, maxN)
	for len(out) < maxN {
		ip, err := pickRandomIPv6(prefix)
		if err != nil {
			return out, err
		}
		if _, dup := tried[ip]; dup {
			// Узкий v6-префикс может зацикливаться — выходим, набрав что есть.
			if len(tried) >= maxN {
				break
			}
			continue
		}
		tried[ip] = struct{}{}
		out = append(out, ip)
	}
	return out, nil
}
