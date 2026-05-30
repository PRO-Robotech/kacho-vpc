// Package kachomock — in-memory реализация CQRS-Repository из
// `internal/repo/kacho`. Используется unit-тестами use-case'ов всех 8
// ресурсов VPC (Network/Subnet/Address/RouteTable/SecurityGroup/Gateway/
// PrivateEndpoint/NetworkInterface). Поддерживает базовую TX-семантику:
//   - Writer накапливает изменения во вспомогательный «uncommitted» state и
//     виден сам себе (G.2 — Get/List после Insert внутри одного writer'а
//     возвращают пишемые данные).
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
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.7): полное покрытие 8 ресурсов.
// Per-resource reader/writer реализации вынесены в отдельные файлы
// (`network.go`, `subnet.go`, `security_group.go`, `route_table.go`,
// `address.go`, `gateway.go`, `private_endpoint.go`, `network_interface.go`).
// Этот файл — центральный glue: `Repository` + `Reader()` / `Writer()` /
// `Commit()` / `Abort()` + общие seed/assertion-методы.
package kachomock

import (
	"context"
	"sort"
	"sync"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
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
// Все 8 ресурсов VPC: networks, securityGroups, subnets, addresses, routeTables,
// privateEndpoints, networkInterfaces, gateways. Все Reader/Writer работают
// единообразно — in-memory state с TX-семантикой (writer accumulate'ит в local
// map, Commit flush'ит в parent state).
type Repository struct {
	mu                sync.Mutex
	networks          map[string]*kacho.NetworkRecord
	securityGroups    map[string]*kacho.SecurityGroupRecord
	subnets           map[string]*kacho.SubnetRecord
	routeTables       map[string]*kacho.RouteTableRecord
	privateEndpoints  map[string]*kacho.PrivateEndpointRecord
	networkInterfaces map[string]*kacho.NetworkInterfaceRecord
	addresses         map[string]*kacho.AddressRecord
	gateways          map[string]*kacho.GatewayRecord
	// addressPools — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6): admin-only ресурс.
	addressPools map[string]*kacho.AddressPoolRecord
	// netDefBinds / addrOverrideBinds — explicit-биндинги pool ↔ network/address.
	netDefBinds       map[string]string // network_id → pool_id
	addrOverrideBinds map[string]string // address_id → pool_id
	// cloudSelectors — admin-controlled routing-labels per Cloud.
	cloudSelectors map[string]*domain.CloudPoolSelector
	outbox         []OutboxEvent
}

// NewRepository создаёт пустой mock-Repository.
func NewRepository() *Repository {
	return &Repository{
		networks:          make(map[string]*kacho.NetworkRecord),
		securityGroups:    make(map[string]*kacho.SecurityGroupRecord),
		subnets:           make(map[string]*kacho.SubnetRecord),
		routeTables:       make(map[string]*kacho.RouteTableRecord),
		privateEndpoints:  make(map[string]*kacho.PrivateEndpointRecord),
		networkInterfaces: make(map[string]*kacho.NetworkInterfaceRecord),
		addresses:         make(map[string]*kacho.AddressRecord),
		gateways:          make(map[string]*kacho.GatewayRecord),
		addressPools:      make(map[string]*kacho.AddressPoolRecord),
		netDefBinds:       make(map[string]string),
		addrOverrideBinds: make(map[string]string),
		cloudSelectors:    make(map[string]*domain.CloudPoolSelector),
	}
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
// проверяют parent-Subnet validation через CQRS-Reader (KAC-94 A.7 sub-PR 3/6:
// NIC use-case'ы больше не зависят от legacy *repo.SubnetRepo peer-port'а —
// существование Subnet проверяется через `kachoRepo.Reader().Subnets().Get`,
// поэтому fixture-Subnet seed'ится прямо в kachomock).
func (r *Repository) SeedSubnet(rec *kacho.SubnetRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subnets[rec.ID] = rec
}

// SeedSecurityGroup / SeedNetwork / SeedNetworkInterface — KAC-239 S2: фикстуры
// для derived-on-read SG.used_by (NIC.security_group_ids ∋ sg,
// networks.default_security_group_id == sg).
func (r *Repository) SeedSecurityGroup(rec *kacho.SecurityGroupRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.securityGroups[rec.ID] = rec
}

func (r *Repository) SeedNetwork(rec *kacho.NetworkRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.networks[rec.ID] = rec
}

func (r *Repository) SeedNetworkInterface(rec *kacho.NetworkInterfaceRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.networkInterfaces[rec.ID] = rec
}

// Outbox возвращает копию выпущенных outbox-event'ов (post-commit only).
func (r *Repository) Outbox() []OutboxEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OutboxEvent, len(r.outbox))
	copy(out, r.outbox)
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

// PrivateEndpoints возвращает копию state'а (для assertions в тестах).
func (r *Repository) PrivateEndpoints() []*kacho.PrivateEndpointRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]*kacho.PrivateEndpointRecord, 0, len(r.privateEndpoints))
	for _, pe := range r.privateEndpoints {
		res = append(res, pe)
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
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
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

// SeedAddressPool — direct insert AddressPoolRecord (тестовый fixture). Wave 5
// replicate (KAC-94 A.7 sub-PR 1/6).
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

// SeedAddressOverrideBinding — direct insert binding.
func (r *Repository) SeedAddressOverrideBinding(addressID, poolID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addrOverrideBinds[addressID] = poolID
}

// SeedCloudPoolSelector — direct insert selector.
func (r *Repository) SeedCloudPoolSelector(s *domain.CloudPoolSelector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cloudSelectors[s.CloudID] = s
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
	peSnap := make(map[string]*kacho.PrivateEndpointRecord, len(r.privateEndpoints))
	for id, pe := range r.privateEndpoints {
		cp := *pe
		peSnap[id] = &cp
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
	aoSnap := make(map[string]string, len(r.addrOverrideBinds))
	for k, v := range r.addrOverrideBinds {
		aoSnap[k] = v
	}
	csSnap := make(map[string]*domain.CloudPoolSelector, len(r.cloudSelectors))
	for id, s := range r.cloudSelectors {
		cp := *s
		csSnap[id] = &cp
	}
	return &readerImpl{
		netSnap:  netSnap,
		sgSnap:   sgSnap,
		subSnap:  subSnap,
		rtSnap:   rtSnap,
		peSnap:   peSnap,
		niSnap:   niSnap,
		addrSnap: addrSnap,
		gwSnap:   gwSnap,
		apSnap:   apSnap,
		ndSnap:   ndSnap,
		aoSnap:   aoSnap,
		csSnap:   csSnap,
	}, nil
}

// Writer открывает RW-«TX». Изменения буферизуются в self.local и видны только
// этому writer'у; на Commit flush в общий state.
func (r *Repository) Writer(_ context.Context) (kacho.RepositoryWriter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Скопировать current state в writer'овский «working set» — writer видит
	// свои writes (G.2) поверх committed-snapshot'а.
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
	localPEs := make(map[string]*kacho.PrivateEndpointRecord, len(r.privateEndpoints))
	for id, pe := range r.privateEndpoints {
		cp := *pe
		localPEs[id] = &cp
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
	localAOs := make(map[string]string, len(r.addrOverrideBinds))
	for k, v := range r.addrOverrideBinds {
		localAOs[k] = v
	}
	localCloudSels := make(map[string]*domain.CloudPoolSelector, len(r.cloudSelectors))
	for id, s := range r.cloudSelectors {
		cp := *s
		localCloudSels[id] = &cp
	}
	return &writerImpl{
		parent:         r,
		local:          localNets,
		localSGs:       localSGs,
		localSubs:      localSubs,
		localRTs:       localRTs,
		localPEs:       localPEs,
		localNIs:       localNIs,
		localAddrs:     localAddrs,
		localGWs:       localGWs,
		localAPs:       localAPs,
		localNDs:       localNDs,
		localAOs:       localAOs,
		localCloudSels: localCloudSels,
	}, nil
}

// Close — no-op.
func (r *Repository) Close() {}

// readerImpl — read-only snapshot. Закрытие — no-op (Mock не держит ресурс).
// Per-resource Reader iface-методы возвращают per-resource структуры (см.
// `network.go`, `subnet.go`, ...).
type readerImpl struct {
	netSnap  map[string]*kacho.NetworkRecord
	sgSnap   map[string]*kacho.SecurityGroupRecord
	subSnap  map[string]*kacho.SubnetRecord
	rtSnap   map[string]*kacho.RouteTableRecord
	peSnap   map[string]*kacho.PrivateEndpointRecord
	niSnap   map[string]*kacho.NetworkInterfaceRecord
	addrSnap map[string]*kacho.AddressRecord
	gwSnap   map[string]*kacho.GatewayRecord
	apSnap   map[string]*kacho.AddressPoolRecord
	ndSnap   map[string]string
	aoSnap   map[string]string
	csSnap   map[string]*domain.CloudPoolSelector
}

func (rd *readerImpl) Networks() kacho.NetworkReaderIface {
	return &networkReader{snap: rd.netSnap}
}

func (rd *readerImpl) SecurityGroups() kacho.SecurityGroupReaderIface {
	// nics/nets — для derived-on-read UsedBy (KAC-239 S2).
	return &securityGroupReader{snap: rd.sgSnap, nics: rd.niSnap, nets: rd.netSnap}
}

func (rd *readerImpl) Subnets() kacho.SubnetReaderIface {
	return &subnetReader{snap: rd.subSnap, addrs: rd.addrSnap}
}

func (rd *readerImpl) RouteTables() kacho.RouteTableReaderIface {
	return &routeTableReader{snap: rd.rtSnap}
}

func (rd *readerImpl) PrivateEndpoints() kacho.PrivateEndpointReaderIface {
	return &privateEndpointReader{snap: rd.peSnap}
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

// AddressPools — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (rd *readerImpl) AddressPools() kacho.AddressPoolReaderIface {
	return &addressPoolReader{snap: rd.apSnap}
}

// AddressPoolBindings — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (rd *readerImpl) AddressPoolBindings() kacho.AddressPoolBindingReaderIface {
	return &addressPoolBindingReader{netDef: rd.ndSnap, addrOver: rd.aoSnap}
}

// CloudPoolSelectors — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (rd *readerImpl) CloudPoolSelectors() kacho.CloudPoolSelectorReaderIface {
	return &cloudPoolSelectorReader{snap: rd.csSnap}
}

func (rd *readerImpl) Close() error { return nil }

// writerImpl — write-«TX». local-* — working set'ы, окончательно мерж'атся в
// parent.<resource> на Commit. local-outbox — буфер outbox-event'ов, на Commit
// добавляется в parent.outbox.
type writerImpl struct {
	parent         *Repository
	local          map[string]*kacho.NetworkRecord
	localSGs       map[string]*kacho.SecurityGroupRecord
	localSubs      map[string]*kacho.SubnetRecord
	localPEs       map[string]*kacho.PrivateEndpointRecord
	localRTs       map[string]*kacho.RouteTableRecord
	localNIs       map[string]*kacho.NetworkInterfaceRecord
	localAddrs     map[string]*kacho.AddressRecord
	localGWs       map[string]*kacho.GatewayRecord
	localAPs       map[string]*kacho.AddressPoolRecord
	localNDs       map[string]string
	localAOs       map[string]string
	localCloudSels map[string]*domain.CloudPoolSelector
	localOutbox    []OutboxEvent
	deletedIDs     map[string]struct{} // Network deletions
	deletedSGIDs   map[string]struct{} // SG deletions
	deletedSubIDs  map[string]struct{} // Subnet deletions
	deletedPEIDs   map[string]struct{} // PE deletions
	deletedRTIDs   map[string]struct{} // RouteTable deletions
	deletedNIIDs   map[string]struct{} // NIC deletions
	deletedAddrIDs map[string]struct{} // Address deletions
	deletedGWIDs   map[string]struct{} // Gateway deletions
	deletedAPIDs   map[string]struct{} // AddressPool deletions
	deletedNDIDs   map[string]struct{} // NetworkDefault binding deletions
	deletedAOIDs   map[string]struct{} // AddressOverride binding deletions
	deletedCSIDs   map[string]struct{} // CloudSelector deletions
	finalised      bool
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

func (w *writerImpl) PrivateEndpoints() kacho.PrivateEndpointWriterIface {
	return &privateEndpointWriter{w: w}
}

func (w *writerImpl) RouteTables() kacho.RouteTableWriterIface {
	return &routeTableWriter{w: w}
}

// NetworkInterfaces возвращает NIC-writer, привязанный к этой «TX». Includes
// atomic AttachToInstance CAS (mirrors KAC-52 repo-side guard — см. iface
// `network_interface.go`) + idempotent DetachFromInstance + Insert.
func (w *writerImpl) NetworkInterfaces() kacho.NetworkInterfaceWriterIface {
	return &networkInterfaceWriter{w: w}
}

func (w *writerImpl) Addresses() kacho.AddressWriterIface {
	return &addressWriter{w: w}
}

func (w *writerImpl) Gateways() kacho.GatewayWriterIface {
	return &gatewayWriter{w: w}
}

// AddressPools — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (w *writerImpl) AddressPools() kacho.AddressPoolWriterIface {
	return &addressPoolWriter{w: w}
}

// AddressPoolBindings — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (w *writerImpl) AddressPoolBindings() kacho.AddressPoolBindingWriterIface {
	return &addressPoolBindingWriter{w: w}
}

// CloudPoolSelectors — Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
func (w *writerImpl) CloudPoolSelectors() kacho.CloudPoolSelectorWriterIface {
	return &cloudPoolSelectorWriter{w: w}
}

func (w *writerImpl) Outbox() kacho.OutboxEmitter {
	return &outboxEmitter{w: w}
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
	// Удалить помеченные на delete (PE).
	for id := range w.deletedPEIDs {
		delete(w.parent.privateEndpoints, id)
	}
	// Применить writes (PE).
	for id, pe := range w.localPEs {
		w.parent.privateEndpoints[id] = pe
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
	// Удалить + apply AddressOverride bindings.
	for id := range w.deletedAOIDs {
		delete(w.parent.addrOverrideBinds, id)
	}
	for k, v := range w.localAOs {
		w.parent.addrOverrideBinds[k] = v
	}
	// Удалить + apply CloudPoolSelector.
	for id := range w.deletedCSIDs {
		delete(w.parent.cloudSelectors, id)
	}
	for id, s := range w.localCloudSels {
		w.parent.cloudSelectors[id] = s
	}
	// Перенести outbox-events в общий state.
	w.parent.outbox = append(w.parent.outbox, w.localOutbox...)
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

// Assertion: Repository удовлетворяет интерфейсу kacho.Repository.
var _ kacho.Repository = (*Repository)(nil)
