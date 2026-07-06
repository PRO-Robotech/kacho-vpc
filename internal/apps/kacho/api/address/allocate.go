// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

// Allocate* — internal-only IPAM allocate (вызывается из
// kacho.cloud.vpc.v1.InternalAddressService). Принимает уже-созданный Address,
// резолвит AddressPool по cascade и атомарно проставляет IP в БД.
//
// Idempotent: если у Address уже есть IP — возвращает его без аллокации.
// Если pools == nil — Allocate* недоступны (test-only setup).
//
// Каждый AllocateXxx открывает writer-TX и делает в ней Get + Set/Allocate +
// Outbox.UPDATED атомарно.

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// AllocateUseCase — internal-only IPAM allocate (4 family-варианта).
//
// Результат allocate-операций — `domain.AllocateResult` (вынесен в domain
// leaf, чтобы port `internal/handler.AddressAllocator` мог ссылаться на этот тип,
// не импортируя use-case-пакет address — dependency-rule: transport зависит от
// domain+port, а не от use-case-конкрета).
//
// Принимает CQRS-порт `Repo`. Каждый Allocate-метод открывает writer-TX и
// делает Get + Set/Allocate + Outbox.UPDATED атомарно.
type AllocateUseCase struct {
	repo         Repo
	subnetReader SubnetReader
	pools        PoolService // nil → Allocate*ExternalIP* недоступны
}

// NewAllocateUseCase создает AllocateUseCase.
func NewAllocateUseCase(r Repo, subnetReader SubnetReader, pools PoolService) *AllocateUseCase {
	return &AllocateUseCase{repo: r, subnetReader: subnetReader, pools: pools}
}

// AllocateInternalIP — выделяет next-free IPv4 в subnet, который указан
// в address.internal_ipv4.subnet_id. Idempotent.
//
// Iterate по ВСЕМ V4CidrBlocks subnet'а: двухфазный allocator (random pick +
// deterministic sweep с tried-set) устраняет false-fail на near-full subnet.
func (u *AllocateUseCase) AllocateInternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// GetForUpdate (row-lock), НЕ plain Get: сериализует read-modify-write
	// internal_ipv4 под конкурентными дублирующими allocate одного address'а.
	// Второй вызов блокируется до commit первого, затем видит уже проставленный
	// internal_ipv4 и возвращает его идемпотентно ниже — вместо software
	// Get→check→unconditional-Set (second-writer-wins, project-rule #10).
	addr, err := w.Addresses().GetForUpdate(ctx, addressID)
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
	// Shared IPAM-цикл (alloc_shared.go) — общий с CreateAddressUseCase.allocateInternalIPv4.
	updated, err := allocateInternalV4IntoTx(ctx, w, u.subnetReader, addr)
	if err != nil {
		return nil, err
	}
	return u.finishAllocate(ctx, w, updated, &domain.AllocateResult{IP: updated.InternalIpv4.Address})
}

// AllocateInternalIPv6 — выделяет случайный свободный IPv6 внутри
// subnet.v6_cidr_blocks[0] для Address с заполненным internal_ipv6.subnet_id.
func (u *AllocateUseCase) AllocateInternalIPv6(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	// GetForUpdate (row-lock), НЕ plain Get: сериализует read-modify-write
	// internal_ipv6 под конкурентными дублирующими allocate одного address'а
	// (см. AllocateInternalIP; project-rule #10).
	addr, err := w.Addresses().GetForUpdate(ctx, addressID)
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
	// Shared IPAM-цикл (alloc_shared.go) — общий с CreateAddressUseCase.allocateInternalIPv6.
	updated, err := allocateInternalV6IntoTx(ctx, w, u.subnetReader, addr)
	if err != nil {
		return nil, err
	}
	return u.finishAllocate(ctx, w, updated, &domain.AllocateResult{IP: updated.InternalIpv6.Address})
}

// AllocateExternalIP — резолвит pool через cascade и выделяет next-free IPv4
// из его freelist (address_pool_free_ips). Idempotent.
//
// PG-native allocator: один SQL-statement (FOR UPDATE SKIP LOCKED → DELETE
// FROM freelist → UPDATE addresses) на каждую попытку. Нулевая contention
// между параллельными аллокаторами; каждая аллокация O(1) по числу IP в pool'е.
func (u *AllocateUseCase) AllocateExternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	// Read + idempotency-check + pool-resolve ДО Writer-TX (отдельная Reader-TX).
	// Иначе resolve открывал бы вторую pool-конн под держимым Writer'ом → deadlock
	// пула под нагрузкой. Атомарность гарантирует freelist-pop с target-guard
	// внутри Writer'а: гонку с конкурентным allocate он отсекает (0 строк →
	// exhausted).
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	addr, err := rd.Addresses().Get(ctx, addressID)
	_ = rd.Close()
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

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()
	// Shared freelist-pop + error-map (alloc_shared.go) — общий с
	// CreateAddressUseCase.allocateExternalIPv4.
	ip, err := allocateExternalV4IntoTx(ctx, w, pool.ID, addressID)
	if err != nil {
		return nil, err
	}
	// Re-read updated record (AllocateIPFromFreelist пишет в addresses внутри
	// writer-TX, видим себе) — для outbox-snapshot'а.
	updated, gerr := w.Addresses().Get(ctx, addressID)
	if gerr != nil {
		return nil, serviceerr.MapRepoErr(gerr)
	}
	return u.finishAllocate(ctx, w, updated, &domain.AllocateResult{IP: ip, PoolID: pool.ID})
}

// AllocateExternalIPv6 — выделяет внешний IPv6 для address через sparse
// counter-based allocator. Зеркало AllocateExternalIP для v4: cascade resolve
// pool → repo.AllocateExternalIPv6 → IP.
func (u *AllocateUseCase) AllocateExternalIPv6(ctx context.Context, addressID string) (*domain.AllocateResult, error) {
	// Read + idempotency + pool-resolve ДО Writer-TX (избегаем nested pool-conn).
	// См. AllocateExternalIP.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	addr, err := rd.Addresses().Get(ctx, addressID)
	_ = rd.Close()
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

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()
	// Shared external-v6 allocate + error-map (alloc_shared.go) — общий с
	// CreateAddressUseCase.allocateExternalIPv6.
	ip, err := allocateExternalV6IntoTx(ctx, w, pool.ID, addressID, addr.ExternalIpv6.ZoneID)
	if err != nil {
		return nil, err
	}
	updated, gerr := w.Addresses().Get(ctx, addressID)
	if gerr != nil {
		return nil, serviceerr.MapRepoErr(gerr)
	}
	return u.finishAllocate(ctx, w, updated, &domain.AllocateResult{IP: ip, PoolID: pool.ID})
}

// finishAllocate — общий эпилог: outbox-emit Address.UPDATED + Commit.
// Атомарно с Set/Allocate в той же writer-TX.
func (u *AllocateUseCase) finishAllocate(ctx context.Context, w Writer, rec *kachorepo.AddressRecord, res *domain.AllocateResult) (*domain.AllocateResult, error) {
	if err := w.Outbox().Emit(ctx, "Address", rec.ID, "UPDATED", helpers.DomainToMap(rec)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	return res, nil
}
