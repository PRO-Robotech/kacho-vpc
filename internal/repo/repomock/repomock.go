// Package repomock содержит in-memory fake-реализации port-интерфейсов из
// `internal/repo` (раньше — `internal/ports`; Wave 5 G.1 / KAC-94) плюс
// helper'ы для ожидания async-Operation'ов. Используется unit-тестами
// `internal/service`, `internal/handler` и use-case-пакетов `internal/apps/kacho/api/*`.
//
// Зависит только от `internal/repo`, `internal/domain` и `kacho-corelib/operations`
// — НЕ от `internal/service`/use-case-пакетов, поэтому white-box service-тесты
// (`package service`) могут импортировать repomock без import-cycle.
package repomock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ---- NetworkRepo ----
//
// Wave 2 pilot (KAC-99/KAC-94): port-интерфейс возвращает *kachorepo.NetworkRecord
// (repo-entity с DB-managed CreatedAt) вместо *domain.Network. Mock хранит
// записи в map[id]*NetworkRecord и проставляет CreatedAt при Insert (`now`).
// Wave 5: NetworkRecord переехал из domain в repo-leaf (`internal/repo/kacho/`).

type NetworkRepo struct {
	mu   sync.Mutex
	data map[string]*kachorepo.NetworkRecord
}

func NewNetworkRepo() *NetworkRepo {
	return &NetworkRepo{data: make(map[string]*kachorepo.NetworkRecord)}
}

func (r *NetworkRepo) Get(_ context.Context, id string) (*kachorepo.NetworkRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return n, nil
}

func (r *NetworkRepo) List(_ context.Context, f repo.NetworkFilter, _ repo.Pagination) ([]*kachorepo.NetworkRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*kachorepo.NetworkRecord
	for _, n := range r.data {
		if (f.ProjectID == "" || n.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(n.Name) == f.Name) {
			result = append(result, n)
		}
	}
	return result, "", nil
}

func (r *NetworkRepo) Insert(_ context.Context, n *domain.Network) (*kachorepo.NetworkRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &kachorepo.NetworkRecord{Network: *n, CreatedAt: time.Now().UTC()}
	r.data[n.ID] = rec
	return rec, nil
}

func (r *NetworkRepo) Update(_ context.Context, n *domain.Network) (*kachorepo.NetworkRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.data[n.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	// keep existing CreatedAt; overwrite mutable domain-fields.
	existing.Network = *n
	return existing, nil
}

func (r *NetworkRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return repo.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *NetworkRepo) SetProjectID(_ context.Context, id, folderID string) (*kachorepo.NetworkRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	n.ProjectID = folderID
	return n, nil
}

// ---- SubnetRepo ----
//
// Wave 2 batch A (KAC-94): port возвращает *kachorepo.SubnetRecord (repo-entity
// с DB-managed CreatedAt). Mock хранит записи в map[id]*SubnetRecord и
// проставляет CreatedAt при Insert. Parity с NetworkRepo (KAC-99).

type SubnetRepo struct {
	mu   sync.Mutex
	data map[string]*kachorepo.SubnetRecord
}

func NewSubnetRepo() *SubnetRepo { return &SubnetRepo{data: make(map[string]*kachorepo.SubnetRecord)} }

func (r *SubnetRepo) Get(_ context.Context, id string) (*kachorepo.SubnetRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return s, nil
}

func (r *SubnetRepo) List(_ context.Context, f repo.SubnetFilter, _ repo.Pagination) ([]*kachorepo.SubnetRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*kachorepo.SubnetRecord
	for _, s := range r.data {
		if (f.ProjectID == "" || s.ProjectID == f.ProjectID) &&
			(f.NetworkID == "" || s.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(s.Name) == f.Name) {
			result = append(result, s)
		}
	}
	return result, "", nil
}

func (r *SubnetRepo) Insert(_ context.Context, s *domain.Subnet) (*kachorepo.SubnetRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &kachorepo.SubnetRecord{Subnet: *s, CreatedAt: time.Now().UTC()}
	r.data[s.ID] = rec
	return rec, nil
}

func (r *SubnetRepo) Update(_ context.Context, s *domain.Subnet) (*kachorepo.SubnetRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.data[s.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	// keep existing CreatedAt; overwrite mutable domain-fields.
	existing.Subnet = *s
	return existing, nil
}

func (r *SubnetRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return repo.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *SubnetRepo) SetProjectID(_ context.Context, id, folderID string) (*kachorepo.SubnetRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	s.ProjectID = folderID
	return s, nil
}

func (r *SubnetRepo) SetCidrBlocks(_ context.Context, id string, v4, v6 []string) (*kachorepo.SubnetRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	s.V4CidrBlocks = v4
	s.V6CidrBlocks = v6
	return s, nil
}

func (r *SubnetRepo) SetZoneID(_ context.Context, id, zoneID string) (*kachorepo.SubnetRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	s.ZoneID = zoneID
	return s, nil
}

func (r *SubnetRepo) AddressesBySubnet(_ context.Context, _ string, _ repo.Pagination) ([]*kachorepo.AddressRecord, string, error) {
	return nil, "", nil
}

// ---- AddressRepo ----
//
// Wave 2 batch A (KAC-94): port возвращает *kachorepo.AddressRecord (repo-entity
// с DB-managed CreatedAt). Mock хранит записи в map[id]*AddressRecord и
// проставляет CreatedAt при Insert.

type AddressRepo struct {
	mu        sync.Mutex
	data      map[string]*kachorepo.AddressRecord
	refs      map[string]*domain.AddressReference // referrer-tracking (addressID → ref)
	freelists map[string][]string                 // poolID → ordered free IPs (FIFO)
	v6        map[string]*v6CursorState           // KAC-60: per-pool v6 sparse counter
}

func NewAddressRepo() *AddressRepo {
	return &AddressRepo{data: make(map[string]*kachorepo.AddressRecord)}
}

// SeedFreelist засыпает poolID-freelist ровно перечисленными IP в указанном
// порядке (для unit-тестов, чтобы не материализовать CIDR целиком).
func (r *AddressRepo) SeedFreelist(poolID string, ips ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.freelists == nil {
		r.freelists = make(map[string][]string)
	}
	r.freelists[poolID] = append([]string(nil), ips...)
}

// Seed добавляет address напрямую в стор (для тестовых fixture'ов). Принимает
// repo-entity (AddressRecord) — caller выставляет CreatedAt сам (либо оставляет
// zero для unit-тестов, где TS не важен).
func (r *AddressRepo) Seed(rec *kachorepo.AddressRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[rec.ID] = rec
}

func (r *AddressRepo) Get(_ context.Context, id string) (*kachorepo.AddressRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return a, nil
}

func (r *AddressRepo) List(_ context.Context, f repo.AddressFilter, _ repo.Pagination) ([]*kachorepo.AddressRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*kachorepo.AddressRecord
	for _, a := range r.data {
		if (f.ProjectID == "" || a.ProjectID == f.ProjectID) &&
			(f.Name == "" || string(a.Name) == f.Name) {
			result = append(result, a)
		}
	}
	return result, "", nil
}

func (r *AddressRepo) Insert(_ context.Context, a *domain.Address) (*kachorepo.AddressRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &kachorepo.AddressRecord{Address: *a, CreatedAt: time.Now().UTC()}
	r.data[a.ID] = rec
	return rec, nil
}

func (r *AddressRepo) Update(_ context.Context, a *domain.Address) (*kachorepo.AddressRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.data[a.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.Address = *a
	return existing, nil
}

func (r *AddressRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return repo.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

// SetIPSpec — mock-stub (порт обязателен, для test'а возвращаем как Update).
func (r *AddressRepo) SetIPSpec(_ context.Context, id string, ext *domain.ExternalIpv4Spec, intn *domain.InternalIpv4Spec) (*kachorepo.AddressRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if ext != nil {
		a.ExternalIpv4 = ext
	}
	if intn != nil {
		a.InternalIpv4 = intn
	}
	return a, nil
}

// SetInternalIPv6 — mock-stub (порт обязателен).
func (r *AddressRepo) SetInternalIPv6(_ context.Context, id string, spec *domain.InternalIpv6Spec) (*kachorepo.AddressRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if spec != nil {
		a.InternalIpv6 = spec
	}
	return a, nil
}

func (r *AddressRepo) SetProjectID(_ context.Context, id, folderID string) (*kachorepo.AddressRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	a.ProjectID = folderID
	return a, nil
}

func (r *AddressRepo) ExistsIP(_ context.Context, ip string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.data {
		if a.ExternalIpv4 != nil && a.ExternalIpv4.Address == ip {
			return true, nil
		}
		if a.InternalIpv4 != nil && a.InternalIpv4.Address == ip {
			return true, nil
		}
	}
	return false, nil
}

func (r *AddressRepo) GetByValue(_ context.Context, ext, intl, _ string) (*kachorepo.AddressRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.data {
		if ext != "" && a.ExternalIpv4 != nil && a.ExternalIpv4.Address == ext {
			return a, nil
		}
		if intl != "" && a.InternalIpv4 != nil && a.InternalIpv4.Address == intl {
			return a, nil
		}
	}
	return nil, repo.ErrNotFound
}

// SetReference upsert'ит referrer-row (если address существует) и выставляет used=true.
// KAC-88: CAS-семантика — если уже есть referrer-row с ДРУГИМ referrer_id →
// ErrFailedPrecondition (parity c repo.AddressRepo.SetReference).
func (r *AddressRepo) SetReference(_ context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[ref.AddressID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if r.refs == nil {
		r.refs = make(map[string]*domain.AddressReference)
	}
	if existing, ok := r.refs[ref.AddressID]; ok && existing.ReferrerID != ref.ReferrerID {
		return nil, repo.ErrFailedPrecondition
	}
	a.Used = true
	cp := *ref
	cp.AttachedAt = time.Now()
	r.refs[ref.AddressID] = &cp
	return &cp, nil
}

// MarkEphemeralInUse атомарно: reserved=false + used=true + upsert referrer-row.
// KAC-88: CAS-семантика — попытка перепривязать к чужому referrer → ErrFailedPrecondition.
func (r *AddressRepo) MarkEphemeralInUse(_ context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[ref.AddressID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if r.refs == nil {
		r.refs = make(map[string]*domain.AddressReference)
	}
	if existing, ok := r.refs[ref.AddressID]; ok && existing.ReferrerID != ref.ReferrerID {
		return nil, repo.ErrFailedPrecondition
	}
	a.Reserved = false
	a.Used = true
	cp := *ref
	cp.AttachedAt = time.Now()
	r.refs[ref.AddressID] = &cp
	return &cp, nil
}

// ClearReference удаляет referrer-row (no-op если нет) и выставляет used=false.
func (r *AddressRepo) ClearReference(_ context.Context, addressID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[addressID]
	if !ok {
		return repo.ErrNotFound
	}
	a.Used = false
	delete(r.refs, addressID)
	return nil
}

// GetReference возвращает referrer-row (ErrNotFound если address или referrer нет).
func (r *AddressRepo) GetReference(_ context.Context, addressID string) (*domain.AddressReference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[addressID]; !ok {
		return nil, repo.ErrNotFound
	}
	ref, ok := r.refs[addressID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *ref
	return &cp, nil
}

// ReferencesForAddresses возвращает referrer-row'ы для набора address-id.
func (r *AddressRepo) ReferencesForAddresses(_ context.Context, addressIDs []string) (map[string]*domain.AddressReference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*domain.AddressReference, len(addressIDs))
	for _, id := range addressIDs {
		if ref, ok := r.refs[id]; ok {
			cp := *ref
			out[id] = &cp
		}
	}
	return out, nil
}

// AllocateIPFromFreelist — mock-stub: pops first IP из freelist, проставляет
// его в addresses.external_ipv4. Возвращает repo.ErrPoolExhausted если
// freelist для pool пуст или не засеян (см. SeedFreelist).
func (r *AddressRepo) AllocateIPFromFreelist(_ context.Context, poolID, addressID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.freelists == nil {
		return "", repo.ErrPoolExhausted
	}
	ips := r.freelists[poolID]
	if len(ips) == 0 {
		return "", repo.ErrPoolExhausted
	}
	ip := ips[0]
	r.freelists[poolID] = ips[1:]
	a, ok := r.data[addressID]
	if !ok {
		return "", repo.ErrNotFound
	}
	if a.ExternalIpv4 == nil {
		a.ExternalIpv4 = &domain.ExternalIpv4Spec{}
	}
	a.ExternalIpv4.Address = ip
	a.ExternalIpv4.AddressPoolID = poolID
	return ip, nil
}

// ReturnIPToFreelist — mock-stub: кладёт IP обратно в freelist; идемпотентно.
func (r *AddressRepo) ReturnIPToFreelist(_ context.Context, poolID, ip string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.freelists == nil {
		r.freelists = make(map[string][]string)
	}
	for _, existing := range r.freelists[poolID] {
		if existing == ip {
			return nil
		}
	}
	r.freelists[poolID] = append(r.freelists[poolID], ip)
	return nil
}

// KAC-60: sparse v6 counter — in-memory stub. Реальная логика — в repo
// (поверх PG-таблиц ipv6_pool_cursors / ipv6_allocated_ips / ipv6_released_offsets).
// Mock держит per-pool monotonic counter; pop released первое.
type v6CursorState struct {
	next     uint64
	released []uint64
	// allocated: address_id → (pool, ip, offset) для FreeExternalIPv6.
	allocated map[string]struct {
		pool   string
		ip     string
		offset uint64
	}
}

func (r *AddressRepo) initV6State(poolID string) *v6CursorState {
	if r.v6 == nil {
		r.v6 = make(map[string]*v6CursorState)
	}
	if _, ok := r.v6[poolID]; !ok {
		r.v6[poolID] = &v6CursorState{next: 1, allocated: map[string]struct {
			pool   string
			ip     string
			offset uint64
		}{}}
	}
	return r.v6[poolID]
}

func (r *AddressRepo) InitIPv6PoolCursor(_ context.Context, poolID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initV6State(poolID)
	return nil
}

func (r *AddressRepo) AllocateExternalIPv6(_ context.Context, poolID, addressID, zoneID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.initV6State(poolID)
	var offset uint64
	if len(st.released) > 0 {
		offset = st.released[0]
		st.released = st.released[1:]
	} else {
		offset = st.next
		st.next++
	}
	// Mock IP: 2001:db8::<offset>. Достаточно для unit/portmock тестов; интеграция
	// — против реальной PG-логики (см. address_repo_ipv6.go).
	ip := fmt.Sprintf("2001:db8::%x", offset)
	st.allocated[addressID] = struct {
		pool   string
		ip     string
		offset uint64
	}{poolID, ip, offset}

	// Зеркалим в a.ExternalIpv6 (как делает реальный repo).
	if a, ok := r.data[addressID]; ok {
		a.ExternalIpv6 = &domain.ExternalIpv6Spec{
			Address: ip, ZoneID: zoneID, AddressPoolID: poolID,
		}
	}
	return ip, nil
}

func (r *AddressRepo) FreeExternalIPv6(_ context.Context, addressID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.v6 == nil {
		return nil
	}
	for poolID, st := range r.v6 {
		if alloc, ok := st.allocated[addressID]; ok {
			st.released = append(st.released, alloc.offset)
			delete(st.allocated, addressID)
			_ = poolID
			break
		}
	}
	if a, ok := r.data[addressID]; ok {
		a.ExternalIpv6 = nil
	}
	return nil
}

// ---- RouteTableRepo ----
//
// Wave 2 batch A (KAC-94): port возвращает *kachorepo.RouteTableRecord (repo-entity
// с DB-managed CreatedAt). Mock хранит записи в map[id]*RouteTableRecord.

type RouteTableRepo struct {
	mu   sync.Mutex
	data map[string]*kachorepo.RouteTableRecord
}

func NewRouteTableRepo() *RouteTableRepo {
	return &RouteTableRepo{data: make(map[string]*kachorepo.RouteTableRecord)}
}

func (r *RouteTableRepo) Get(_ context.Context, id string) (*kachorepo.RouteTableRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return rt, nil
}

func (r *RouteTableRepo) List(_ context.Context, f repo.RouteTableFilter, _ repo.Pagination) ([]*kachorepo.RouteTableRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*kachorepo.RouteTableRecord
	for _, rt := range r.data {
		if (f.ProjectID == "" || rt.ProjectID == f.ProjectID) &&
			(f.NetworkID == "" || rt.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(rt.Name) == f.Name) {
			result = append(result, rt)
		}
	}
	return result, "", nil
}

func (r *RouteTableRepo) Insert(_ context.Context, rt *domain.RouteTable) (*kachorepo.RouteTableRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &kachorepo.RouteTableRecord{RouteTable: *rt, CreatedAt: time.Now().UTC()}
	r.data[rt.ID] = rec
	return rec, nil
}

func (r *RouteTableRepo) Update(_ context.Context, rt *domain.RouteTable) (*kachorepo.RouteTableRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.data[rt.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.RouteTable = *rt
	return existing, nil
}

func (r *RouteTableRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return repo.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *RouteTableRepo) SetProjectID(_ context.Context, id, folderID string) (*kachorepo.RouteTableRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	rt.ProjectID = folderID
	return rt, nil
}

// ---- SecurityGroupRepo ----
//
// Wave 2 batch B (KAC-94): port возвращает *kachorepo.SecurityGroupRecord (repo-entity
// с DB-managed CreatedAt). Parity с SubnetRepo (KAC-94 batch A).

type SecurityGroupRepo struct {
	mu   sync.Mutex
	data map[string]*kachorepo.SecurityGroupRecord
}

func NewSecurityGroupRepo() *SecurityGroupRepo {
	return &SecurityGroupRepo{data: make(map[string]*kachorepo.SecurityGroupRecord)}
}

func (r *SecurityGroupRepo) Get(_ context.Context, id string) (*kachorepo.SecurityGroupRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return sg, nil
}

func (r *SecurityGroupRepo) List(_ context.Context, f repo.SecurityGroupFilter, _ repo.Pagination) ([]*kachorepo.SecurityGroupRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*kachorepo.SecurityGroupRecord
	for _, sg := range r.data {
		if f.ProjectID != "" && sg.ProjectID != f.ProjectID {
			continue
		}
		if f.NetworkID != "" && sg.NetworkID != f.NetworkID {
			continue
		}
		if f.Name != "" && sg.Name != domain.RcNameVPC(f.Name) {
			continue
		}
		out = append(out, sg)
	}
	return out, "", nil
}

func (r *SecurityGroupRepo) Insert(_ context.Context, sg *domain.SecurityGroup) (*kachorepo.SecurityGroupRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &kachorepo.SecurityGroupRecord{SecurityGroup: *sg, CreatedAt: time.Now().UTC()}
	r.data[sg.ID] = rec
	return rec, nil
}

func (r *SecurityGroupRepo) Update(_ context.Context, sg *domain.SecurityGroup) (*kachorepo.SecurityGroupRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.data[sg.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.SecurityGroup = *sg
	return existing, nil
}

func (r *SecurityGroupRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *SecurityGroupRepo) UpdateRules(_ context.Context, sgID string, _ []string, _ []domain.SecurityGroupRule) (*kachorepo.SecurityGroupRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return sg, nil
}

func (r *SecurityGroupRepo) UpdateRule(_ context.Context, sgID, _ string, _ string, _ map[string]string, _ []string) (*kachorepo.SecurityGroupRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return sg, nil
}

func (r *SecurityGroupRepo) SetProjectID(_ context.Context, id, folderID string) (*kachorepo.SecurityGroupRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	sg.ProjectID = folderID
	return sg, nil
}

// ---- GatewayRepo ----
//
// Wave 2 batch B (KAC-94): port возвращает *kachorepo.GatewayRecord.

type GatewayRepo struct {
	mu   sync.Mutex
	data map[string]*kachorepo.GatewayRecord
}

func NewGatewayRepo() *GatewayRepo {
	return &GatewayRepo{data: make(map[string]*kachorepo.GatewayRecord)}
}

func (r *GatewayRepo) Get(_ context.Context, id string) (*kachorepo.GatewayRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return g, nil
}

func (r *GatewayRepo) List(_ context.Context, f repo.GatewayFilter, _ repo.Pagination) ([]*kachorepo.GatewayRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*kachorepo.GatewayRecord
	for _, g := range r.data {
		if f.ProjectID != "" && g.ProjectID != f.ProjectID {
			continue
		}
		if f.Name != "" && g.Name != domain.RcNameVPC(f.Name) {
			continue
		}
		out = append(out, g)
	}
	return out, "", nil
}

func (r *GatewayRepo) Insert(_ context.Context, g *domain.Gateway) (*kachorepo.GatewayRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &kachorepo.GatewayRecord{Gateway: *g, CreatedAt: time.Now().UTC()}
	r.data[g.ID] = rec
	return rec, nil
}

func (r *GatewayRepo) Update(_ context.Context, g *domain.Gateway) (*kachorepo.GatewayRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.data[g.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.Gateway = *g
	return existing, nil
}

func (r *GatewayRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *GatewayRepo) SetProjectID(_ context.Context, id, folderID string) (*kachorepo.GatewayRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	g.ProjectID = folderID
	return g, nil
}

// ---- PrivateEndpointRepo ----
//
// Wave 5 replicate (KAC-94): port возвращает *kachorepo.PrivateEndpointRecord
// (repo-entity уехал из domain в repo-leaf `internal/repo/kacho/entity_private_endpoint.go`).

type PrivateEndpointRepo struct {
	mu   sync.Mutex
	data map[string]*kachorepo.PrivateEndpointRecord
}

func NewPrivateEndpointRepo() *PrivateEndpointRepo {
	return &PrivateEndpointRepo{data: make(map[string]*kachorepo.PrivateEndpointRecord)}
}

func (r *PrivateEndpointRepo) Get(_ context.Context, id string) (*kachorepo.PrivateEndpointRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.data[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return p, nil
}

func (r *PrivateEndpointRepo) List(_ context.Context, f repo.PrivateEndpointFilter, _ repo.Pagination) ([]*kachorepo.PrivateEndpointRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*kachorepo.PrivateEndpointRecord
	for _, p := range r.data {
		if f.ProjectID != "" && p.ProjectID != f.ProjectID {
			continue
		}
		if f.Name != "" && p.Name != domain.RcNameVPC(f.Name) {
			continue
		}
		out = append(out, p)
	}
	return out, "", nil
}

func (r *PrivateEndpointRepo) Insert(_ context.Context, p *domain.PrivateEndpoint) (*kachorepo.PrivateEndpointRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &kachorepo.PrivateEndpointRecord{PrivateEndpoint: *p, CreatedAt: time.Now().UTC()}
	r.data[p.ID] = rec
	return rec, nil
}

func (r *PrivateEndpointRepo) Update(_ context.Context, p *domain.PrivateEndpoint) (*kachorepo.PrivateEndpointRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.data[p.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.PrivateEndpoint = *p
	return existing, nil
}

func (r *PrivateEndpointRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

// ---- ProjectClient ----

// ProjectClient — fake ProjectClient. OK задаёт результат Exists(); CloudID —
// результат GetCloudIDFromProject() (по умолчанию "" — NotFound-семантика).
type ProjectClient struct {
	OK      bool
	CloudID string
}

func (c *ProjectClient) Exists(_ context.Context, _ string) (bool, error) { return c.OK, nil }

func (c *ProjectClient) GetCloudIDFromProject(_ context.Context, _ string) (string, error) { return c.CloudID, nil }

// ---- ZoneRegistry ----

type ZoneRegistry struct {
	Known []string // zone_id, которые считаются существующими в таблице `zones`
}

func NewZoneRegistry(ids ...string) *ZoneRegistry { return &ZoneRegistry{Known: ids} }

func (m *ZoneRegistry) Get(_ context.Context, id string) (*domain.Zone, error) {
	for _, k := range m.Known {
		if k == id {
			return &domain.Zone{ID: id}, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (m *ZoneRegistry) ListIDs(_ context.Context) ([]string, error) {
	out := make([]string, len(m.Known))
	copy(out, m.Known)
	return out, nil
}

// ---- operations.Repo ----

// OpsRepo — in-memory реализация kacho-corelib/operations.Repo.
type OpsRepo struct {
	mu  sync.Mutex
	ops map[string]*operations.Operation
}

func NewOpsRepo() *OpsRepo { return &OpsRepo{ops: make(map[string]*operations.Operation)} }

func (r *OpsRepo) Create(_ context.Context, op operations.Operation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops[op.ID] = &op
	return nil
}

func (r *OpsRepo) CreateWithPrincipal(_ context.Context, op operations.Operation, p operations.Principal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op.Principal = p
	r.ops[op.ID] = &op
	return nil
}

func (r *OpsRepo) Get(_ context.Context, id string) (*operations.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return nil, operations.ErrNotFound
	}
	// Возвращаем shallow-копию — чтобы caller не читал shared-state
	// после release lock'a (race с MarkDone/MarkError из worker-горутины).
	cp := *op
	return &cp, nil
}

func (r *OpsRepo) List(_ context.Context, _ operations.ListFilter) ([]operations.Operation, string, error) {
	return nil, "", nil
}

func (r *OpsRepo) MarkDone(_ context.Context, id string, resp *anypb.Any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return operations.ErrNotFound
	}
	op.Done = true
	op.Response = resp
	return nil
}

func (r *OpsRepo) MarkError(_ context.Context, id string, errStatus *status.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return operations.ErrNotFound
	}
	op.Done = true
	op.Error = errStatus
	return nil
}

func (r *OpsRepo) Cancel(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return operations.ErrNotFound
	}
	op.Done = true
	return nil
}

// ---- await-helpers для async Operation worker'ов ----

// TestingT — минимальный интерфейс из *testing.T/*testing.B, нужный
// await-helper'ам. Принимаем интерфейс чтобы не импортировать testing.
type TestingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// AwaitOpDone детерминированно ждёт завершения worker-горутины (Operation.Done
// == true). Заменяет фиксированный time.Sleep (см. TODO #10). Падает через 2s.
func AwaitOpDone(t TestingT, r *OpsRepo, opID string) *operations.Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		op, err := r.Get(context.Background(), opID)
		if err == nil && op.Done {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s did not finish within 2s", opID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// AwaitAllOpsDone ждёт пока все ops в repo станут Done. Удобно когда тест не
// сохраняет конкретный opID. Падает через 2s.
func AwaitAllOpsDone(t TestingT, r *OpsRepo) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		allDone := true
		var stuckID string
		for id, op := range r.ops {
			if !op.Done {
				allDone = false
				stuckID = id
				break
			}
		}
		r.mu.Unlock()
		if allDone {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s did not finish within 2s", stuckID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Compile-time проверки соответствия peer-service port-интерфейсам.
//
// Wave 5 finalize (KAC-94, skill evgeniy §6 G.6): legacy resource-repo
// интерфейсы (`NetworkRepoIface` / `SubnetRepoIface` / …) удалены из
// `internal/repo/iface.go`. Mock-структуры здесь продолжают существовать как
// in-memory adapter'ы под **узкие port'ы** use-case-пакетов (skill evgeniy
// §6 G.2) — каждый use-case-пакет описывает свой Repo-интерфейс локально,
// mock структура удовлетворяет его duck-typing'ом. Compile-time assertion на
// удалённый общий интерфейс не нужен.
//
// Peer-service порты `ProjectClient` / `ZoneRegistry` — кросс-сервисные,
// общие для всех use-case'ов, поэтому остаются в общем `internal/repo`-пакете
// и compile-проверяются здесь.
var (
	_ repo.ProjectClient = (*ProjectClient)(nil)
	_ repo.ZoneRegistry = (*ZoneRegistry)(nil)
	_ operations.Repo   = (*OpsRepo)(nil)
)
