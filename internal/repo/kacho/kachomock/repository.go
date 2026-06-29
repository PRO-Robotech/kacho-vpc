// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package kachomock — in-memory реализация CQRS-Repository из
// `internal/repo/kacho`. Используется unit-тестами use-case'ов
// ресурсов VPC (Network/Subnet/Address/RouteTable/SecurityGroup/Gateway/
// NetworkInterface). Поддерживает базовую TX-семантику:
//   - Writer накапливает изменения во вспомогательный «uncommitted» state и
//     виден сам себе (Get/List после Insert внутри одного writer'а возвращают
//     пишемые данные).
//   - На Commit — flush в общий state.
//   - На Abort (или просто без Commit) — uncommitted state выкидывается.
//   - Параллельный Reader НЕ видит uncommitted writes (read-committed).
//   - Outbox-emit транзакционен (если Abort — события писались бы только во
//     внутренний буфер, который выкидывается).
//
// Mock сознательно НЕ покрывает 100% pgxpool-семантики (нет MVCC, нет lock'ов,
// нет ON CONFLICT). Его задача — проверить, что use-case-код корректно
// открывает TX, делает Commit/Abort, и outbox-emit на правильных путях.
//
// Per-resource reader/writer реализации вынесены в отдельные файлы
// (`network.go`, `subnet.go`, `security_group.go`, `route_table.go`,
// `address.go`, `gateway.go`, `network_interface.go`).
// Этот файл — центральный glue: `Repository` + `Reader()` / `Writer()` /
// `Commit()` / `Abort()` + общие seed/assertion-методы.
package kachomock

import (
	"context"
	"sort"
	"sync"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// OutboxEvent — снимок outbox-row (для проверок в тестах: что было emit'ed и
// в каком порядке).
type OutboxEvent struct {
	Resource string
	ID       string
	Action   string
	Payload  map[string]any
}

// Repository — in-memory mock корневого CQRS-контракта. Потокобезопасный
// (sync.Mutex на общем state — нужен для concurrent integration-like тестов).
//
// Все ресурсы VPC: networks, securityGroups, subnets, addresses, routeTables,
// networkInterfaces, gateways. Все Reader/Writer работают
// единообразно — in-memory state с TX-семантикой (writer accumulate'ит в local
// map, Commit flush'ит в parent state).
type Repository struct {
	mu                sync.Mutex
	networks          map[string]*kacho.NetworkRecord
	securityGroups    map[string]*kacho.SecurityGroupRecord
	subnets           map[string]*kacho.SubnetRecord
	routeTables       map[string]*kacho.RouteTableRecord
	networkInterfaces map[string]*kacho.NetworkInterfaceRecord
	addresses         map[string]*kacho.AddressRecord
	gateways          map[string]*kacho.GatewayRecord
	// addressPools — admin-only ресурс.
	addressPools map[string]*kacho.AddressPoolRecord
	// anycastPools — tenant-facing project-scoped пулы anycast-адресов.
	anycastPools map[string]*kacho.AnycastAddressPoolRecord
	// anycastAttach — pivot пул↔сеть: pool_id → set(network_id).
	anycastAttach map[string]map[string]struct{}
	// anycastAlloc — seed-override живых anycast-аллокаций: "pool_id|network_id" → n.
	anycastAlloc map[string]int64
	// netDefBinds — explicit-биндинги pool ↔ network (network_default).
	netDefBinds map[string]string // network_id → pool_id
	// allocatedInCidr — override для CountAllocatedInCidrs(poolID, *): тест-фикстура
	// сообщает «в этом пуле есть N выделенных external-IP» без моделирования
	// freelist. Ключ — poolID. Mock игнорирует cidr-аргумент.
	allocatedInCidr map[string]int64
	// freelistAddedCidrs — запись вызовов AddCidrToFreelist (poolID → добавленные
	// cidrs) для проверки в unit-тесте AddCidrBlocks.
	freelistAddedCidrs map[string][]string
	outbox             []OutboxEvent
	// fgaRegister — зафиксированные FGA-register/unregister intent'ы (post-commit),
	// для проверок в unit-тестах что DML-путь эмитит правильный owner-tuple в той
	// же writer-TX.
	fgaRegister []FGARegisterEvent
}

// FGARegisterEvent — снимок одной fga_register_outbox-строки (для проверок в
// unit-тестах: какой tuple, mirror feed (labels+parent) и под каким event_type
// был эмитнут в writer-TX).
type FGARegisterEvent struct {
	EventType       string
	Tuple           fgaregister.Tuple
	Labels          map[string]string
	ParentProjectID string
}

// NewRepository создает пустой mock-Repository.
func NewRepository() *Repository {
	return &Repository{
		networks:           make(map[string]*kacho.NetworkRecord),
		securityGroups:     make(map[string]*kacho.SecurityGroupRecord),
		subnets:            make(map[string]*kacho.SubnetRecord),
		routeTables:        make(map[string]*kacho.RouteTableRecord),
		networkInterfaces:  make(map[string]*kacho.NetworkInterfaceRecord),
		addresses:          make(map[string]*kacho.AddressRecord),
		gateways:           make(map[string]*kacho.GatewayRecord),
		addressPools:       make(map[string]*kacho.AddressPoolRecord),
		anycastPools:       make(map[string]*kacho.AnycastAddressPoolRecord),
		anycastAttach:      make(map[string]map[string]struct{}),
		anycastAlloc:       make(map[string]int64),
		netDefBinds:        make(map[string]string),
		allocatedInCidr:    make(map[string]int64),
		freelistAddedCidrs: make(map[string][]string),
	}
}

// SeedAllocatedInCidr помечает пул как имеющий n выделенных external-IPv4 (для
// негативного теста RemoveCidrBlocks). Mock возвращает n из
// CountAllocatedInCidrs(poolID, *) независимо от cidr-аргумента.
func (r *Repository) SeedAllocatedInCidr(poolID string, n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allocatedInCidr[poolID] = n
}

// FreelistAddedCidrs возвращает зафиксированные вызовы AddCidrToFreelist для
// пула (после Commit).
func (r *Repository) FreelistAddedCidrs(poolID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.freelistAddedCidrs[poolID]))
	copy(out, r.freelistAddedCidrs[poolID])
	return out
}

// ResetFreelistAdds очищает запись freelist-add для пула (тест отделяет дельту
// AddCidrBlocks от первичного populate в Create).
func (r *Repository) ResetFreelistAdds(poolID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.freelistAddedCidrs, poolID)
}

// SeedAddress добавляет AddressRecord в Address-state (для тестов
// AddressesBySubnet / ListUsedAddresses). Mock не имеет AddressRepo, поэтому
// fixture seed'ится напрямую.
func (r *Repository) SeedAddress(rec *kacho.AddressRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addresses[rec.ID] = rec
}

// SeedSubnet добавляет SubnetRecord в Subnet-state. Нужен тестам, которые
// проверяют parent-Subnet validation через CQRS-Reader: NIC use-case'ы проверяют
// существование Subnet через `kachoRepo.Reader().Subnets().Get`, поэтому
// fixture-Subnet seed'ится прямо в kachomock.
func (r *Repository) SeedSubnet(rec *kacho.SubnetRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subnets[rec.ID] = rec
}

// Outbox возвращает копию выпущенных outbox-event'ов (post-commit only).
func (r *Repository) Outbox() []OutboxEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OutboxEvent, len(r.outbox))
	copy(out, r.outbox)
	return out
}

// FGARegisterEvents возвращает копию выпущенных FGA-register-intent'ов
// (post-commit only) — для unit-assertion «Create/Delete эмитит owner-tuple
// в writer-TX».
func (r *Repository) FGARegisterEvents() []FGARegisterEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]FGARegisterEvent, len(r.fgaRegister))
	copy(out, r.fgaRegister)
	return out
}

// Networks возвращает копию state'а (для assertions в тестах).
func (r *Repository) Networks() []*kacho.NetworkRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.NetworkRecord, 0, len(r.networks))
	for _, n := range r.networks {
		res = append(res, n)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// SecurityGroups возвращает копию state'а (для assertions в тестах).
func (r *Repository) SecurityGroups() []*kacho.SecurityGroupRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.SecurityGroupRecord, 0, len(r.securityGroups))
	for _, sg := range r.securityGroups {
		res = append(res, sg)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// Subnets возвращает копию Subnet state'а (для assertions в тестах).
func (r *Repository) Subnets() []*kacho.SubnetRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.SubnetRecord, 0, len(r.subnets))
	for _, s := range r.subnets {
		res = append(res, s)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// RouteTables возвращает копию state'а (для assertions в тестах).
func (r *Repository) RouteTables() []*kacho.RouteTableRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.RouteTableRecord, 0, len(r.routeTables))
	for _, rt := range r.routeTables {
		res = append(res, rt)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// NetworkInterfaces возвращает копию state'а (для assertions в тестах).
func (r *Repository) NetworkInterfaces() []*kacho.NetworkInterfaceRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.NetworkInterfaceRecord, 0, len(r.networkInterfaces))
	for _, ni := range r.networkInterfaces {
		res = append(res, ni)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// AddressPools возвращает копию state'а (для assertions в тестах).
func (r *Repository) AddressPools() []*kacho.AddressPoolRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.AddressPoolRecord, 0, len(r.addressPools))
	for _, p := range r.addressPools {
		res = append(res, p)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// SeedAddressPool — direct insert AddressPoolRecord (тестовый fixture).
func (r *Repository) SeedAddressPool(rec *kacho.AddressPoolRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addressPools[rec.ID] = rec
}

// SeedNetworkDefaultBinding — direct insert binding.
func (r *Repository) SeedNetworkDefaultBinding(networkID, poolID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.netDefBinds[networkID] = poolID
}

// SeedAnycastPool — direct insert AnycastAddressPoolRecord (тестовый fixture).
func (r *Repository) SeedAnycastPool(rec *kacho.AnycastAddressPoolRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.anycastPools[rec.ID] = rec
}

// SeedAnycastAttachment — direct insert pivot пул↔сеть (тестовый fixture).
func (r *Repository) SeedAnycastAttachment(poolID, networkID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.anycastAttach[poolID] == nil {
		r.anycastAttach[poolID] = make(map[string]struct{})
	}
	r.anycastAttach[poolID][networkID] = struct{}{}
}

// SeedAnycastAllocation — пометить, что у пула n живых anycast-аллокаций в сети
// (для негативного detach-теста). Mock возвращает n из CountAllocationsInNetwork.
func (r *Repository) SeedAnycastAllocation(poolID, networkID string, n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.anycastAlloc[poolID+"|"+networkID] = n
}

// AnycastPools возвращает копию state'а (для assertions в тестах).
func (r *Repository) AnycastPools() []*kacho.AnycastAddressPoolRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.AnycastAddressPoolRecord, 0, len(r.anycastPools))
	for _, p := range r.anycastPools {
		res = append(res, p)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// AnycastAttachments возвращает network_id'ы, к которым приаттачен пул (для
// assertions в тестах).
func (r *Repository) AnycastAttachments(poolID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.anycastAttach[poolID]))
	for nid := range r.anycastAttach[poolID] {
		out = append(out, nid)
	}
	sort.Strings(out)
	return out
}

// Gateways возвращает копию state'а (для assertions в тестах).
func (r *Repository) Gateways() []*kacho.GatewayRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.GatewayRecord, 0, len(r.gateways))
	for _, g := range r.gateways {
		res = append(res, g)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// Reader открывает read-only «TX». Snapshot текущего committed state'а
// заfreez'ен на момент открытия — параллельный Writer не виден этому Reader'у
// (read-committed semantics).
func (r *Repository) Reader(_ context.Context) (kacho.RepositoryReader, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	netSnap := make(map[string]*kacho.NetworkRecord, len(r.networks))
	for id, n := range r.networks {
		cp := *n
		netSnap[id] = &cp
	}
	sgSnap := make(map[string]*kacho.SecurityGroupRecord, len(r.securityGroups))
	for id, sg := range r.securityGroups {
		cp := *sg
		sgSnap[id] = &cp
	}
	subSnap := make(map[string]*kacho.SubnetRecord, len(r.subnets))
	for id, s := range r.subnets {
		cp := *s
		subSnap[id] = &cp
	}
	rtSnap := make(map[string]*kacho.RouteTableRecord, len(r.routeTables))
	for id, rt := range r.routeTables {
		cp := *rt
		rtSnap[id] = &cp
	}
	niSnap := make(map[string]*kacho.NetworkInterfaceRecord, len(r.networkInterfaces))
	for id, ni := range r.networkInterfaces {
		cp := *ni
		niSnap[id] = &cp
	}
	addrSnap := make(map[string]*kacho.AddressRecord, len(r.addresses))
	for id, a := range r.addresses {
		cp := *a
		addrSnap[id] = &cp
	}
	gwSnap := make(map[string]*kacho.GatewayRecord, len(r.gateways))
	for id, g := range r.gateways {
		cp := *g
		gwSnap[id] = &cp
	}
	apSnap := make(map[string]*kacho.AddressPoolRecord, len(r.addressPools))
	for id, p := range r.addressPools {
		cp := *p
		apSnap[id] = &cp
	}
	ndSnap := make(map[string]string, len(r.netDefBinds))
	for k, v := range r.netDefBinds {
		ndSnap[k] = v
	}
	aapSnap := make(map[string]*kacho.AnycastAddressPoolRecord, len(r.anycastPools))
	for id, p := range r.anycastPools {
		cp := *p
		aapSnap[id] = &cp
	}
	aapAttachSnap := copyAttach(r.anycastAttach)
	aapAllocSnap := make(map[string]int64, len(r.anycastAlloc))
	for k, v := range r.anycastAlloc {
		aapAllocSnap[k] = v
	}
	return &readerImpl{
		netSnap:       netSnap,
		sgSnap:        sgSnap,
		subSnap:       subSnap,
		rtSnap:        rtSnap,
		niSnap:        niSnap,
		addrSnap:      addrSnap,
		gwSnap:        gwSnap,
		apSnap:        apSnap,
		ndSnap:        ndSnap,
		aapSnap:       aapSnap,
		aapAttachSnap: aapAttachSnap,
		aapAllocSnap:  aapAllocSnap,
	}, nil
}

// copyAttach делает глубокую копию pivot-map'ы пул↔сеть.
func copyAttach(src map[string]map[string]struct{}) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{}, len(src))
	for pool, nets := range src {
		inner := make(map[string]struct{}, len(nets))
		for nid := range nets {
			inner[nid] = struct{}{}
		}
		out[pool] = inner
	}
	return out
}

// Writer открывает RW-«TX». Изменения буферизуются в self.local и видны только
// этому writer'у; на Commit flush в общий state.
func (r *Repository) Writer(_ context.Context) (kacho.RepositoryWriter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Скопировать current state в writer'овский «working set» — writer видит
	// свои writes поверх committed-snapshot'а.
	localNets := make(map[string]*kacho.NetworkRecord, len(r.networks))
	for id, n := range r.networks {
		cp := *n
		localNets[id] = &cp
	}
	localSGs := make(map[string]*kacho.SecurityGroupRecord, len(r.securityGroups))
	for id, sg := range r.securityGroups {
		cp := *sg
		localSGs[id] = &cp
	}
	localSubs := make(map[string]*kacho.SubnetRecord, len(r.subnets))
	for id, s := range r.subnets {
		cp := *s
		localSubs[id] = &cp
	}
	localRTs := make(map[string]*kacho.RouteTableRecord, len(r.routeTables))
	for id, rt := range r.routeTables {
		cp := *rt
		localRTs[id] = &cp
	}
	localNIs := make(map[string]*kacho.NetworkInterfaceRecord, len(r.networkInterfaces))
	for id, ni := range r.networkInterfaces {
		cp := *ni
		localNIs[id] = &cp
	}
	localAddrs := make(map[string]*kacho.AddressRecord, len(r.addresses))
	for id, a := range r.addresses {
		cp := *a
		localAddrs[id] = &cp
	}
	localGWs := make(map[string]*kacho.GatewayRecord, len(r.gateways))
	for id, g := range r.gateways {
		cp := *g
		localGWs[id] = &cp
	}
	localAPs := make(map[string]*kacho.AddressPoolRecord, len(r.addressPools))
	for id, p := range r.addressPools {
		cp := *p
		localAPs[id] = &cp
	}
	localNDs := make(map[string]string, len(r.netDefBinds))
	for k, v := range r.netDefBinds {
		localNDs[k] = v
	}
	localAAPs := make(map[string]*kacho.AnycastAddressPoolRecord, len(r.anycastPools))
	for id, p := range r.anycastPools {
		cp := *p
		localAAPs[id] = &cp
	}
	localAAPAttach := copyAttach(r.anycastAttach)
	return &writerImpl{
		parent:         r,
		local:          localNets,
		localSGs:       localSGs,
		localSubs:      localSubs,
		localRTs:       localRTs,
		localNIs:       localNIs,
		localAddrs:     localAddrs,
		localGWs:       localGWs,
		localAPs:       localAPs,
		localNDs:       localNDs,
		localAAPs:      localAAPs,
		localAAPAttach: localAAPAttach,
	}, nil
}

// Close — no-op.
func (r *Repository) Close() {}

// readerImpl — read-only snapshot. Закрытие — no-op (Mock не держит ресурс).
// Per-resource Reader iface-методы возвращают per-resource структуры (см.
// `network.go`, `subnet.go`, ...).
type readerImpl struct {
	netSnap       map[string]*kacho.NetworkRecord
	sgSnap        map[string]*kacho.SecurityGroupRecord
	subSnap       map[string]*kacho.SubnetRecord
	rtSnap        map[string]*kacho.RouteTableRecord
	niSnap        map[string]*kacho.NetworkInterfaceRecord
	addrSnap      map[string]*kacho.AddressRecord
	gwSnap        map[string]*kacho.GatewayRecord
	apSnap        map[string]*kacho.AddressPoolRecord
	ndSnap        map[string]string
	aapSnap       map[string]*kacho.AnycastAddressPoolRecord
	aapAttachSnap map[string]map[string]struct{}
	aapAllocSnap  map[string]int64
}

func (rd *readerImpl) Networks() kacho.NetworkReaderIface {
	return &networkReader{snap: rd.netSnap}
}

func (rd *readerImpl) SecurityGroups() kacho.SecurityGroupReaderIface {
	return &securityGroupReader{snap: rd.sgSnap}
}

func (rd *readerImpl) Subnets() kacho.SubnetReaderIface {
	return &subnetReader{snap: rd.subSnap, addrs: rd.addrSnap}
}

func (rd *readerImpl) RouteTables() kacho.RouteTableReaderIface {
	return &routeTableReader{snap: rd.rtSnap}
}

func (rd *readerImpl) NetworkInterfaces() kacho.NetworkInterfaceReaderIface {
	return &networkInterfaceReader{snap: rd.niSnap}
}

func (rd *readerImpl) Addresses() kacho.AddressReaderIface {
	return &addressReader{snap: rd.addrSnap}
}

func (rd *readerImpl) Gateways() kacho.GatewayReaderIface {
	return &gatewayReader{snap: rd.gwSnap}
}

func (rd *readerImpl) AddressPools() kacho.AddressPoolReaderIface {
	return &addressPoolReader{snap: rd.apSnap}
}

func (rd *readerImpl) AddressPoolBindings() kacho.AddressPoolBindingReaderIface {
	return &addressPoolBindingReader{netDef: rd.ndSnap}
}

func (rd *readerImpl) AnycastAddressPools() kacho.AnycastAddressPoolReaderIface {
	return &anycastAddressPoolReader{snap: rd.aapSnap, attach: rd.aapAttachSnap, alloc: rd.aapAllocSnap}
}

func (rd *readerImpl) Close() error { return nil }

// writerImpl — write-«TX». local-* — working set'ы, окончательно мерж'атся в
// parent.<resource> на Commit. local-outbox — буфер outbox-event'ов, на Commit
// добавляется в parent.outbox.
type writerImpl struct {
	parent     *Repository
	local      map[string]*kacho.NetworkRecord
	localSGs   map[string]*kacho.SecurityGroupRecord
	localSubs  map[string]*kacho.SubnetRecord
	localRTs   map[string]*kacho.RouteTableRecord
	localNIs   map[string]*kacho.NetworkInterfaceRecord
	localAddrs map[string]*kacho.AddressRecord
	localGWs   map[string]*kacho.GatewayRecord
	localAPs   map[string]*kacho.AddressPoolRecord
	localNDs   map[string]string
	// localAAPs / localAAPAttach — working-set anycast-пулов и pivot'ов, flush в
	// parent на Commit (TX-семантика: Abort выкидывает).
	localAAPs      map[string]*kacho.AnycastAddressPoolRecord
	localAAPAttach map[string]map[string]struct{}
	deletedAAPIDs  map[string]struct{} // AnycastAddressPool deletions
	// localFreelistAdds — буфер AddCidrToFreelist-вызовов, flush в
	// parent.freelistAddedCidrs на Commit.
	localFreelistAdds map[string][]string
	localOutbox       []OutboxEvent
	localFGARegister  []FGARegisterEvent
	deletedIDs        map[string]struct{} // Network deletions
	deletedSGIDs      map[string]struct{} // SG deletions
	deletedSubIDs     map[string]struct{} // Subnet deletions
	deletedRTIDs      map[string]struct{} // RouteTable deletions
	deletedNIIDs      map[string]struct{} // NIC deletions
	deletedAddrIDs    map[string]struct{} // Address deletions
	deletedGWIDs      map[string]struct{} // Gateway deletions
	deletedAPIDs      map[string]struct{} // AddressPool deletions
	deletedNDIDs      map[string]struct{} // NetworkDefault binding deletions
	finalised         bool
}

func (w *writerImpl) Networks() kacho.NetworkWriterIface {
	return &networkWriter{w: w}
}

func (w *writerImpl) SecurityGroups() kacho.SecurityGroupWriterIface {
	return &securityGroupWriter{w: w}
}

func (w *writerImpl) Subnets() kacho.SubnetWriterIface {
	return &subnetWriter{w: w}
}

func (w *writerImpl) RouteTables() kacho.RouteTableWriterIface {
	return &routeTableWriter{w: w}
}

// NetworkInterfaces возвращает NIC-writer, привязанный к этой «TX».
func (w *writerImpl) NetworkInterfaces() kacho.NetworkInterfaceWriterIface {
	return &networkInterfaceWriter{w: w}
}

func (w *writerImpl) Addresses() kacho.AddressWriterIface {
	return &addressWriter{w: w}
}

func (w *writerImpl) Gateways() kacho.GatewayWriterIface {
	return &gatewayWriter{w: w}
}

func (w *writerImpl) AddressPools() kacho.AddressPoolWriterIface {
	return &addressPoolWriter{w: w}
}

func (w *writerImpl) AddressPoolBindings() kacho.AddressPoolBindingWriterIface {
	return &addressPoolBindingWriter{w: w}
}

func (w *writerImpl) AnycastAddressPools() kacho.AnycastAddressPoolWriterIface {
	return &anycastAddressPoolWriter{w: w}
}

func (w *writerImpl) Outbox() kacho.OutboxEmitter {
	return &outboxEmitter{w: w}
}

// FGARegister — in-memory FGA-register-intent emitter. Накапливает в
// localFGARegister (flush в parent.fgaRegister на Commit) — TX-семантика та же,
// что у Outbox (Abort выкидывает intent'ы вместе с DML).
func (w *writerImpl) FGARegister() kacho.FGARegisterEmitter {
	return &fgaRegisterEmitter{w: w}
}

func (w *writerImpl) Commit() error {
	if w.finalised {
		return nil
	}
	w.finalised = true
	w.parent.mu.Lock()
	defer w.parent.mu.Unlock()
	// Удалить помеченные на delete (Network).
	for id := range w.deletedIDs {
		delete(w.parent.networks, id)
	}
	// Применить writes (Network).
	for id, n := range w.local {
		// Если id был помечен на удаление и сразу re-added в этом writer'е — упустим
		// этот edge-case (не используется в pilot'ных тестах).
		w.parent.networks[id] = n
	}
	// Удалить помеченные на delete (SG).
	for id := range w.deletedSGIDs {
		delete(w.parent.securityGroups, id)
	}
	// Применить writes (SG).
	for id, sg := range w.localSGs {
		w.parent.securityGroups[id] = sg
	}
	// Удалить помеченные на delete (Subnet).
	for id := range w.deletedSubIDs {
		delete(w.parent.subnets, id)
	}
	// Применить writes (Subnet).
	for id, s := range w.localSubs {
		w.parent.subnets[id] = s
	}
	// Удалить помеченные на delete (RT).
	for id := range w.deletedRTIDs {
		delete(w.parent.routeTables, id)
	}
	// Применить writes (RT).
	for id, rt := range w.localRTs {
		w.parent.routeTables[id] = rt
	}
	// Удалить помеченные на delete (NIC).
	for id := range w.deletedNIIDs {
		delete(w.parent.networkInterfaces, id)
	}
	// Применить writes (NIC).
	for id, ni := range w.localNIs {
		w.parent.networkInterfaces[id] = ni
	}
	// Удалить помеченные на delete (Address).
	for id := range w.deletedAddrIDs {
		delete(w.parent.addresses, id)
	}
	// Применить writes (Address).
	for id, a := range w.localAddrs {
		w.parent.addresses[id] = a
	}
	// Удалить помеченные на delete (Gateway).
	for id := range w.deletedGWIDs {
		delete(w.parent.gateways, id)
	}
	// Применить writes (Gateway).
	for id, g := range w.localGWs {
		w.parent.gateways[id] = g
	}
	// Удалить помеченные на delete (AddressPool).
	for id := range w.deletedAPIDs {
		delete(w.parent.addressPools, id)
	}
	// Применить writes (AddressPool).
	for id, p := range w.localAPs {
		w.parent.addressPools[id] = p
	}
	// Удалить + apply NetworkDefault bindings.
	for id := range w.deletedNDIDs {
		delete(w.parent.netDefBinds, id)
	}
	for k, v := range w.localNDs {
		w.parent.netDefBinds[k] = v
	}
	// Удалить помеченные на delete (AnycastAddressPool).
	for id := range w.deletedAAPIDs {
		delete(w.parent.anycastPools, id)
		delete(w.parent.anycastAttach, id)
	}
	// Применить writes (AnycastAddressPool) + заменить pivot целиком (захватывает
	// attach/detach).
	for id, p := range w.localAAPs {
		w.parent.anycastPools[id] = p
	}
	if w.localAAPAttach != nil {
		w.parent.anycastAttach = copyAttach(w.localAAPAttach)
	}
	// Flush freelist-add записей (для AddCidrBlocks unit-теста).
	for poolID, cidrs := range w.localFreelistAdds {
		w.parent.freelistAddedCidrs[poolID] = append(w.parent.freelistAddedCidrs[poolID], cidrs...)
	}
	// Перенести outbox-events в общий state.
	w.parent.outbox = append(w.parent.outbox, w.localOutbox...)
	// Перенести FGA-register-intent'ы в общий state.
	w.parent.fgaRegister = append(w.parent.fgaRegister, w.localFGARegister...)
	return nil
}

func (w *writerImpl) Abort() {
	if w.finalised {
		return
	}
	w.finalised = true
	// Discard local-* + localOutbox (no-op — just drop references).
}

// ---- Outbox emitter ----

type outboxEmitter struct {
	w *writerImpl
}

func (e *outboxEmitter) Emit(_ context.Context, resource, id, action string, payload map[string]any) error {
	// Скопируем payload (caller может его мутировать после Emit).
	cp := make(map[string]any, len(payload))
	for k, v := range payload {
		cp[k] = v
	}
	e.w.localOutbox = append(e.w.localOutbox, OutboxEvent{
		Resource: resource, ID: id, Action: action, Payload: cp,
	})
	return nil
}

// ---- FGA-register-intent emitter ----

type fgaRegisterEmitter struct {
	w *writerImpl
}

func (e *fgaRegisterEmitter) EmitRegister(_ context.Context, intent fgaregister.Intent) error {
	return e.record(fgaregister.EventRegister, intent)
}

func (e *fgaRegisterEmitter) EmitUnregister(_ context.Context, intent fgaregister.Intent) error {
	return e.record(fgaregister.EventUnregister, intent)
}

// record накапливает по одному FGARegisterEvent на Item (one row per item,
// parity с pg-impl). Несет mirror feed (labels+parent). Flush в
// parent.fgaRegister на Commit.
func (e *fgaRegisterEmitter) record(eventType string, intent fgaregister.Intent) error {
	for _, it := range intent.Items {
		e.w.localFGARegister = append(e.w.localFGARegister, FGARegisterEvent{
			EventType:       eventType,
			Tuple:           it.Tuple,
			Labels:          it.Labels,
			ParentProjectID: it.ParentProjectID,
		})
	}
	return nil
}

// Assertion: Repository удовлетворяет интерфейсу kacho.Repository.
var _ kacho.Repository = (*Repository)(nil)
