package domain

import "time"

// Repo-entities — структуры, **физически живущие в `internal/repo/*`**, но
// объявленные здесь, чтобы их мог типизировать ещё и `internal/ports` без
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

// NetworkRecord — repo-entity для Network. domain.Network + CreatedAt
// (DB-managed). Service-слой получает *NetworkRecord из repo.NetworkRepo
// (port-интерфейс) и пробрасывает в DTO/handler. Клиенту через proto уходит
// CreatedAt из этой структуры (skill evgeniy §4 D.1 / §7 H.1).
//
// Имя `Record` (а не Entity / Persistence) — чтобы не пересечься с другими
// доменными терминами; «row из таблицы networks» = `NetworkRecord`.
type NetworkRecord struct {
	Network
	CreatedAt time.Time
}

// SubnetRecord — repo-entity для Subnet. domain.Subnet + CreatedAt (DB-managed).
// Wave 2 batch A (KAC-94) — parity с NetworkRecord. См. doc-комментарий на
// NetworkRecord выше.
type SubnetRecord struct {
	Subnet
	CreatedAt time.Time
}

// AddressRecord — repo-entity для Address. domain.Address + CreatedAt (DB-managed).
// Wave 2 batch A (KAC-94).
type AddressRecord struct {
	Address
	CreatedAt time.Time
}

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

// GatewayRecord — repo-entity для Gateway. domain.Gateway + CreatedAt
// (DB-managed). Wave 2 batch B (KAC-94).
type GatewayRecord struct {
	Gateway
	CreatedAt time.Time
}

// PrivateEndpointRecord — repo-entity для PrivateEndpoint. domain.PrivateEndpoint
// + CreatedAt (DB-managed). Wave 2 batch B (KAC-94).
type PrivateEndpointRecord struct {
	PrivateEndpoint
	CreatedAt time.Time
}

// NetworkInterfaceRecord — repo-entity для NetworkInterface. domain.NetworkInterface
// + CreatedAt (DB-managed). Wave 2 batch C (KAC-94).
type NetworkInterfaceRecord struct {
	NetworkInterface
	CreatedAt time.Time
}
