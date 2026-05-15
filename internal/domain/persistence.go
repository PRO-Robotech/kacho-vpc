package domain

import "time"

// Repo-entities — структуры, **физически живущие в `internal/repo/*`**, но
// объявленные здесь, чтобы их мог типизировать ещё и `internal/ports` без
// import-cycle `ports → repo`. Каждая repo-entity = domain-сущность + DB-managed
// поля (`CreatedAt` ; в будущем — `UpdatedAt`, `Generation`, `Revision`).
//
// Это **временный** compromise для Wave 2 pilot (KAC-99/KAC-94). На Wave 3
// (Фаза 5 — CQRS Repository, skill evgeniy §6) leaf-пакет под repo-entities
// будет выделен явно (`internal/repo/<resource>/entity.go`) — тогда отсюда
// типы уедут. Сейчас держим здесь как self-contained marker «у domain.X
// рядом живёт X-repo-entity, добавляющая CreatedAt».
//
// Импорт: домен сам ни от чего не зависит (skill §1 A.5), здесь только stdlib
// `time` — это сохраняет принцип clean architecture.

// NetworkRecord — repo-entity для Network. domain.Network + CreatedAt
// (DB-managed). Service-слой получает *NetworkRecord из repo.NetworkRepo
// (port-интерфейс) и пробрасывает в DTO/handler. Клиенту через proto уходит
// CreatedAt из этой структуры (skill §4 D.1 / §7 H.1).
//
// Имя `Record` (а не Entity / Persistence) — чтобы не пересечься с другими
// доменными терминами; «row из таблицы networks» = `NetworkRecord`.
type NetworkRecord struct {
	Network
	CreatedAt time.Time
}
