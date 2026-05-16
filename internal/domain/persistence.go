package domain

import "time"

// Repo-entities — структуры, **физически живущие в `internal/repo/*`**, но
// объявленные здесь, чтобы их мог типизировать ещё и `internal/repo` без
// import-cycle `ports → repo`. Каждая repo-entity = domain-сущность + DB-managed
// поля (`CreatedAt` ; в будущем — `UpdatedAt`, `Generation`, `Revision`).
//
// Это **временный** compromise для Wave 2 (KAC-94). На Wave 3 (Фаза 5 — CQRS
// Repository, skill evgeniy §6) leaf-пакет под repo-entities будет выделен явно
// (`internal/repo/<resource>/entity.go`) — тогда отсюда типы уедут. Сейчас
// держим здесь как self-contained marker «у domain.X рядом живёт X-repo-entity,
// добавляющая CreatedAt».
//
// Импорт: домен сам ни от чего не зависит (skill §1 A.5), здесь только stdlib
// `time` — это сохраняет принцип clean architecture.
//
// Wave 5 (KAC-94, skill evgeniy §4 D.1): `NetworkRecord`, `SubnetRecord`,
// `AddressRecord`, `PrivateEndpointRecord`, `NetworkInterfaceRecord` уехали из
// domain в repo-leaf — теперь живут как `kacho.<X>Record` в
// `internal/repo/kacho/entity_<x>.go`. Остаются здесь только
// `RouteTableRecord`/`SecurityGroupRecord`/`GatewayRecord` — RouteTable и
// Gateway уже частично мигрированы через separate `kacho.<X>Record` (см.
// `entity_route_table.go` / `entity_gateway.go`), но domain-копии оставлены
// до удаления последних `domain.<X>Record` referenсes по всему коду;
// SecurityGroup — мигрируется в финальном subtask Wave 5 replicate (SG own
// use-cases CQRS).

// Wave 5 replicate (KAC-94): AddressRecord уехал в repo-leaf
// `internal/repo/kacho/entity_address.go` как `kacho.AddressRecord` (parity
// с NetworkRecord). Здесь его больше нет — все consumer'ы импортируют
// `kacho.AddressRecord` напрямую.

// RouteTableRecord — repo-entity для RouteTable. domain.RouteTable + CreatedAt
// (DB-managed). Wave 2 batch A (KAC-94).
type RouteTableRecord struct {
	RouteTable
	CreatedAt time.Time
}

// SecurityGroupRecord — repo-entity для SecurityGroup. domain.SecurityGroup +
// CreatedAt (DB-managed). Wave 2 batch B (KAC-94).
type SecurityGroupRecord struct {
	SecurityGroup
	CreatedAt time.Time
}

// Wave 5 replicate (KAC-94): GatewayRecord уехал в repo-leaf
// `internal/repo/kacho/entity_gateway.go` как `kacho.GatewayRecord` (parity с
// pilot NetworkRecord, §4 D.1 / §7 H.1). Здесь его больше нет — все consumer'ы
// импортируют `kacho.GatewayRecord` напрямую.

// Wave 5 replicate (KAC-94): PrivateEndpointRecord уехал в repo-leaf
// `internal/repo/kacho/entity_private_endpoint.go` как
// `kacho.PrivateEndpointRecord` (parity с NetworkRecord). Здесь его больше нет
// — все consumer'ы импортируют `kacho.PrivateEndpointRecord` напрямую.

// Wave 5 replicate (KAC-94, NIC batch): NetworkInterfaceRecord уехал в repo-leaf
// `internal/repo/kacho/entity_network_interface.go` как
// `kacho.NetworkInterfaceRecord` (parity с NetworkRecord). Здесь его больше нет
// — все consumer'ы импортируют `kacho.NetworkInterfaceRecord` напрямую.
