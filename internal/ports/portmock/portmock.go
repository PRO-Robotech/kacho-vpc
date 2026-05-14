// Package portmock содержит in-memory fake-реализации port-интерфейсов из
// `internal/ports` плюс helper'ы для ожидания async-Operation'ов. Используется
// unit-тестами `internal/service` и `internal/handler` — раньше каждый
// test-файл держал собственную копию mock'ов (см. TODO #12).
//
// Зависит только от `internal/ports`, `internal/domain` и `kacho-corelib/operations`
// — НЕ от `internal/service`, поэтому white-box service-тесты (`package service`)
// могут его импортировать без import-cycle.
package portmock

import (
	"fmt"
	"context"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// ---- NetworkRepo ----

type NetworkRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Network
}

func NewNetworkRepo() *NetworkRepo { return &NetworkRepo{data: make(map[string]*domain.Network)} }

func (r *NetworkRepo) Get(_ context.Context, id string) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return n, nil
}

func (r *NetworkRepo) List(_ context.Context, f ports.NetworkFilter, _ ports.Pagination) ([]*domain.Network, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Network
	for _, n := range r.data {
		if (f.FolderID == "" || n.FolderID == f.FolderID) &&
			(f.Name == "" || n.Name == f.Name) {
			result = append(result, n)
		}
	}
	return result, "", nil
}

func (r *NetworkRepo) Insert(_ context.Context, n *domain.Network) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[n.ID] = n
	return n, nil
}

func (r *NetworkRepo) Update(_ context.Context, n *domain.Network) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[n.ID]; !ok {
		return nil, ports.ErrNotFound
	}
	r.data[n.ID] = n
	return n, nil
}

func (r *NetworkRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ports.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *NetworkRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	n.FolderID = folderID
	return n, nil
}

// ---- SubnetRepo ----

type SubnetRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Subnet
}

func NewSubnetRepo() *SubnetRepo { return &SubnetRepo{data: make(map[string]*domain.Subnet)} }

func (r *SubnetRepo) Get(_ context.Context, id string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return s, nil
}

func (r *SubnetRepo) List(_ context.Context, f ports.SubnetFilter, _ ports.Pagination) ([]*domain.Subnet, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Subnet
	for _, s := range r.data {
		if (f.FolderID == "" || s.FolderID == f.FolderID) &&
			(f.NetworkID == "" || s.NetworkID == f.NetworkID) &&
			(f.Name == "" || s.Name == f.Name) {
			result = append(result, s)
		}
	}
	return result, "", nil
}

func (r *SubnetRepo) Insert(_ context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[s.ID] = s
	return s, nil
}

func (r *SubnetRepo) Update(_ context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[s.ID]; !ok {
		return nil, ports.ErrNotFound
	}
	r.data[s.ID] = s
	return s, nil
}

func (r *SubnetRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ports.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *SubnetRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	s.FolderID = folderID
	return s, nil
}

func (r *SubnetRepo) SetCidrBlocks(_ context.Context, id string, v4, v6 []string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	s.V4CidrBlocks = v4
	s.V6CidrBlocks = v6
	return s, nil
}

func (r *SubnetRepo) SetZoneID(_ context.Context, id, zoneID string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	s.ZoneID = zoneID
	return s, nil
}

func (r *SubnetRepo) AddressesBySubnet(_ context.Context, _ string, _ ports.Pagination) ([]*domain.Address, string, error) {
	return nil, "", nil
}

// ---- AddressRepo ----

type AddressRepo struct {
	mu        sync.Mutex
	data      map[string]*domain.Address
	refs      map[string]*domain.AddressReference // referrer-tracking (addressID → ref)
	freelists map[string][]string                 // poolID → ordered free IPs (FIFO)
	v6        map[string]*v6CursorState           // KAC-60: per-pool v6 sparse counter
}

func NewAddressRepo() *AddressRepo { return &AddressRepo{data: make(map[string]*domain.Address)} }

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

// Seed добавляет address напрямую в стор (для тестовых fixture'ов).
func (r *AddressRepo) Seed(a *domain.Address) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[a.ID] = a
}

func (r *AddressRepo) Get(_ context.Context, id string) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return a, nil
}

func (r *AddressRepo) List(_ context.Context, f ports.AddressFilter, _ ports.Pagination) ([]*domain.Address, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Address
	for _, a := range r.data {
		if (f.FolderID == "" || a.FolderID == f.FolderID) &&
			(f.Name == "" || a.Name == f.Name) {
			result = append(result, a)
		}
	}
	return result, "", nil
}

func (r *AddressRepo) Insert(_ context.Context, a *domain.Address) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[a.ID] = a
	return a, nil
}

func (r *AddressRepo) Update(_ context.Context, a *domain.Address) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[a.ID]; !ok {
		return nil, ports.ErrNotFound
	}
	r.data[a.ID] = a
	return a, nil
}

func (r *AddressRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ports.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

// SetIPSpec — mock-stub (порт обязателен, для test'а возвращаем как Update).
func (r *AddressRepo) SetIPSpec(_ context.Context, id string, ext *domain.ExternalIpv4Spec, intn *domain.InternalIpv4Spec) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
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
func (r *AddressRepo) SetInternalIPv6(_ context.Context, id string, spec *domain.InternalIpv6Spec) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	if spec != nil {
		a.InternalIpv6 = spec
	}
	return a, nil
}

func (r *AddressRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	a.FolderID = folderID
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

func (r *AddressRepo) GetByValue(_ context.Context, ext, intl, _ string) (*domain.Address, error) {
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
	return nil, ports.ErrNotFound
}

// SetReference upsert'ит referrer-row (если address существует) и выставляет used=true.
func (r *AddressRepo) SetReference(_ context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[ref.AddressID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	a.Used = true
	if r.refs == nil {
		r.refs = make(map[string]*domain.AddressReference)
	}
	cp := *ref
	cp.AttachedAt = time.Now()
	r.refs[ref.AddressID] = &cp
	return &cp, nil
}

// MarkEphemeralInUse атомарно: reserved=false + used=true + upsert referrer-row.
func (r *AddressRepo) MarkEphemeralInUse(_ context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[ref.AddressID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	a.Reserved = false
	a.Used = true
	if r.refs == nil {
		r.refs = make(map[string]*domain.AddressReference)
	}
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
		return ports.ErrNotFound
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
		return nil, ports.ErrNotFound
	}
	ref, ok := r.refs[addressID]
	if !ok {
		return nil, ports.ErrNotFound
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
// его в addresses.external_ipv4. Возвращает ports.ErrPoolExhausted если
// freelist для pool пуст или не засеян (см. SeedFreelist).
func (r *AddressRepo) AllocateIPFromFreelist(_ context.Context, poolID, addressID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.freelists == nil {
		return "", ports.ErrPoolExhausted
	}
	ips := r.freelists[poolID]
	if len(ips) == 0 {
		return "", ports.ErrPoolExhausted
	}
	ip := ips[0]
	r.freelists[poolID] = ips[1:]
	a, ok := r.data[addressID]
	if !ok {
		return "", ports.ErrNotFound
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

type RouteTableRepo struct {
	mu   sync.Mutex
	data map[string]*domain.RouteTable
}

func NewRouteTableRepo() *RouteTableRepo {
	return &RouteTableRepo{data: make(map[string]*domain.RouteTable)}
}

func (r *RouteTableRepo) Get(_ context.Context, id string) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return rt, nil
}

func (r *RouteTableRepo) List(_ context.Context, f ports.RouteTableFilter, _ ports.Pagination) ([]*domain.RouteTable, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.RouteTable
	for _, rt := range r.data {
		if (f.FolderID == "" || rt.FolderID == f.FolderID) &&
			(f.NetworkID == "" || rt.NetworkID == f.NetworkID) &&
			(f.Name == "" || rt.Name == f.Name) {
			result = append(result, rt)
		}
	}
	return result, "", nil
}

func (r *RouteTableRepo) Insert(_ context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[rt.ID] = rt
	return rt, nil
}

func (r *RouteTableRepo) Update(_ context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[rt.ID]; !ok {
		return nil, ports.ErrNotFound
	}
	r.data[rt.ID] = rt
	return rt, nil
}

func (r *RouteTableRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ports.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *RouteTableRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	rt.FolderID = folderID
	return rt, nil
}

// ---- SecurityGroupRepo ----

type SecurityGroupRepo struct {
	mu   sync.Mutex
	data map[string]*domain.SecurityGroup
}

func NewSecurityGroupRepo() *SecurityGroupRepo {
	return &SecurityGroupRepo{data: make(map[string]*domain.SecurityGroup)}
}

func (r *SecurityGroupRepo) Get(_ context.Context, id string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return sg, nil
}

func (r *SecurityGroupRepo) List(_ context.Context, f ports.SecurityGroupFilter, _ ports.Pagination) ([]*domain.SecurityGroup, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.SecurityGroup
	for _, sg := range r.data {
		if f.FolderID != "" && sg.FolderID != f.FolderID {
			continue
		}
		if f.NetworkID != "" && sg.NetworkID != f.NetworkID {
			continue
		}
		if f.Name != "" && sg.Name != f.Name {
			continue
		}
		out = append(out, sg)
	}
	return out, "", nil
}

func (r *SecurityGroupRepo) Insert(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[sg.ID] = sg
	return sg, nil
}

func (r *SecurityGroupRepo) Update(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[sg.ID] = sg
	return sg, nil
}

func (r *SecurityGroupRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *SecurityGroupRepo) UpdateRules(_ context.Context, sgID string, _ []string, _ []domain.SecurityGroupRule) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return sg, nil
}

func (r *SecurityGroupRepo) UpdateRule(_ context.Context, sgID, _ string, _ string, _ map[string]string, _ []string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return sg, nil
}

func (r *SecurityGroupRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	sg.FolderID = folderID
	return sg, nil
}

// ---- GatewayRepo ----

type GatewayRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Gateway
}

func NewGatewayRepo() *GatewayRepo { return &GatewayRepo{data: make(map[string]*domain.Gateway)} }

func (r *GatewayRepo) Get(_ context.Context, id string) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return g, nil
}

func (r *GatewayRepo) List(_ context.Context, f ports.GatewayFilter, _ ports.Pagination) ([]*domain.Gateway, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Gateway
	for _, g := range r.data {
		if f.FolderID != "" && g.FolderID != f.FolderID {
			continue
		}
		if f.Name != "" && g.Name != f.Name {
			continue
		}
		out = append(out, g)
	}
	return out, "", nil
}

func (r *GatewayRepo) Insert(_ context.Context, g *domain.Gateway) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[g.ID] = g
	return g, nil
}

func (r *GatewayRepo) Update(_ context.Context, g *domain.Gateway) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[g.ID] = g
	return g, nil
}

func (r *GatewayRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *GatewayRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	g.FolderID = folderID
	return g, nil
}

// ---- PrivateEndpointRepo ----

type PrivateEndpointRepo struct {
	mu   sync.Mutex
	data map[string]*domain.PrivateEndpoint
}

func NewPrivateEndpointRepo() *PrivateEndpointRepo {
	return &PrivateEndpointRepo{data: make(map[string]*domain.PrivateEndpoint)}
}

func (r *PrivateEndpointRepo) Get(_ context.Context, id string) (*domain.PrivateEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return p, nil
}

func (r *PrivateEndpointRepo) List(_ context.Context, f ports.PrivateEndpointFilter, _ ports.Pagination) ([]*domain.PrivateEndpoint, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.PrivateEndpoint
	for _, p := range r.data {
		if f.FolderID != "" && p.FolderID != f.FolderID {
			continue
		}
		if f.Name != "" && p.Name != f.Name {
			continue
		}
		out = append(out, p)
	}
	return out, "", nil
}

func (r *PrivateEndpointRepo) Insert(_ context.Context, p *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[p.ID] = p
	return p, nil
}

func (r *PrivateEndpointRepo) Update(_ context.Context, p *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[p.ID] = p
	return p, nil
}

func (r *PrivateEndpointRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

// ---- FolderClient ----

// FolderClient — fake FolderClient. OK задаёт результат Exists(); CloudID —
// результат GetCloudID() (по умолчанию "" — NotFound-семантика).
type FolderClient struct {
	OK      bool
	CloudID string
}

func (c *FolderClient) Exists(_ context.Context, _ string) (bool, error) { return c.OK, nil }

func (c *FolderClient) GetCloudID(_ context.Context, _ string) (string, error) { return c.CloudID, nil }

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
	return nil, ports.ErrNotFound
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

// Compile-time проверки соответствия port-интерфейсам.
var (
	_ ports.NetworkRepo         = (*NetworkRepo)(nil)
	_ ports.SubnetRepo          = (*SubnetRepo)(nil)
	_ ports.AddressRepo         = (*AddressRepo)(nil)
	_ ports.RouteTableRepo      = (*RouteTableRepo)(nil)
	_ ports.SecurityGroupRepo   = (*SecurityGroupRepo)(nil)
	_ ports.GatewayRepo         = (*GatewayRepo)(nil)
	_ ports.PrivateEndpointRepo = (*PrivateEndpointRepo)(nil)
	_ ports.FolderClient        = (*FolderClient)(nil)
	_ ports.ZoneRegistry        = (*ZoneRegistry)(nil)
	_ operations.Repo           = (*OpsRepo)(nil)
)
