// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressFilter — фильтр для списка адресов. Лежит в пакете kacho вместе с
// Pagination / NetworkFilter / SecurityGroupFilter (см. doc-комментарий на
// Pagination).
type AddressFilter struct {
	ProjectID string
	Name      string
	Filter    string
	// SubnetID — фильтр по подсети: матчит internal_ipv4.subnet_id ИЛИ
	// internal_ipv6.subnet_id (для ListAddresses?subnet_id=). "" = без фильтра.
	SubnetID string
}

// AddressReaderIface — read-операции над Address в read-only TX-области
// (единый CQRS-контракт, parity с остальными VPC-ресурсами).
type AddressReaderIface interface {
	Get(ctx context.Context, id string) (*AddressRecord, error)
	List(ctx context.Context, f AddressFilter, p Pagination) ([]*AddressRecord, string, error)
	// GetByValue — lookup-by-IP (external/internal). subnetID — optional scope.
	// ErrNotFound если адреса не существует.
	GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*AddressRecord, error)
	// ExistsIP — uniqueness-check IP в БД (external или internal). Используется
	// AddressService для sync-проверки уникальности.
	ExistsIP(ctx context.Context, ip string) (bool, error)
	// GetReference возвращает referrer-row адреса. ErrNotFound если address
	// не существует ИЛИ у него нет referrer'а.
	GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error)
	// ReferencesForAddresses — batch lookup referrer'ов для набора address-id
	// (map id→ref; отсутствующие ключи = нет referrer'а). Пустой вход → пустой map.
	ReferencesForAddresses(ctx context.Context, addressIDs []string) (map[string]*domain.AddressReference, error)
	// ListByIDs — per-object filtered List (WHERE id = ANY), pagination после
	// фильтра. Пустой allowedIDs → (nil, "", nil).
	ListByIDs(ctx context.Context, f AddressFilter, allowedIDs []string, p Pagination) ([]*AddressRecord, string, error)
}

// AddressWriterIface — write-операции + read (writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через RepositoryWriter.Outbox().Emit(...) после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance).
//
// Address имеет специфические writer-методы для IPAM allocate-flow:
//   - SetIPSpec — атомарное обновление external_ipv4 / internal_ipv4 JSONB-spec
//     (random-pick allocator: каждая попытка — отдельный SetIPSpec через writer).
//   - SetInternalIPv6 — то же для v6.
//   - AllocateIPFromFreelist / ReturnIPToFreelist — PG-native freelist allocator (v4).
//   - InitIPv6PoolCursor / AllocateExternalIPv6 / FreeExternalIPv6 — sparse v6 allocator.
//   - SetReference / MarkEphemeralInUse / ClearReference — referrer-tracking (CAS на upsert).
//
// Атомарность IPAM-flow: весь allocate (cascade resolve pool → allocate IP →
// emit Address.UPDATED outbox) идет в одной writer-TX. Use-case открывает
// writer, делает Insert + Allocate* + Outbox().Emit, потом Commit (либо Abort
// при error → Insert откатывается, компенсирующий delete не нужен).
type AddressWriterIface interface {
	AddressReaderIface
	Insert(ctx context.Context, a *domain.Address) (*AddressRecord, error)
	Update(ctx context.Context, a *domain.Address) (*AddressRecord, error)
	// GetForUpdate — Get с `SELECT ... FOR UPDATE` (row-lock) внутри writer-TX.
	// Сериализует read-modify-write в Update (doUpdate): конкурентный Update
	// блокируется на GetForUpdate до commit первого, затем читает уже обновлённый
	// row и применяет свою маску поверх — lost-update исключён (project-rule #10).
	GetForUpdate(ctx context.Context, id string) (*AddressRecord, error)
	Delete(ctx context.Context, id string) error
	// DeleteGuarded — атомарный CAS-delete: удаляет адрес ТОЛЬКО если он не
	// used и не deletion_protection, и возвращает удаленный record (свежий
	// snapshot — для return-to-freelist). Закрывает гонку между sync-проверкой
	// «in use / protected» и worker-DELETE: address_references → addresses ON
	// DELETE CASCADE, поэтому безусловный DELETE молча отцеплял бы
	// конкурентно приаттаченный NIC. 0 строк:
	//   used=true           → ErrFailedPrecondition "address %s is in use"
	//   deletion_protection → ErrFailedPrecondition "...deletion_protection..."
	//   нет строки          → ErrNotFound
	DeleteGuarded(ctx context.Context, id string) (*AddressRecord, error)
	// SetIPSpec атомарно обновляет external_ipv4 / internal_ipv4 JSONB-spec.
	// nil-spec — поле не меняется; оба nil — no-op.
	SetIPSpec(ctx context.Context, id string, externalIpv4 *domain.ExternalIpv4Spec, internalIpv4 *domain.InternalIpv4Spec) (*AddressRecord, error)
	// SetInternalIPv6 атомарно обновляет internal_ipv6 JSONB-spec. nil → no-op.
	SetInternalIPv6(ctx context.Context, id string, spec *domain.InternalIpv6Spec) (*AddressRecord, error)

	// AllocateIPFromFreelist — PG-native v4 allocator: atomic pop из
	// address_pool_free_ips (FOR UPDATE SKIP LOCKED) + UPDATE
	// addresses.external_ipv4{address, address_pool_id}. ErrPoolExhausted если
	// freelist пуст.
	AllocateIPFromFreelist(ctx context.Context, poolID, addressID string) (string, error)
	// ReturnIPToFreelist кладет IP обратно в pool freelist. Идемпотентно
	// (ON CONFLICT DO NOTHING).
	ReturnIPToFreelist(ctx context.Context, poolID, ip string) error

	// InitIPv6PoolCursor инициализирует sparse counter-based allocator для
	// IPv6-пула. Идемпотентно (ON CONFLICT DO NOTHING).
	InitIPv6PoolCursor(ctx context.Context, poolID string) error
	// AllocateExternalIPv6 — sparse v6 allocator: pop released offset → fresh
	// counter → INSERT allocated → UPDATE addresses.external_ipv6 (все в этой
	// writer-TX). ErrPoolExhausted если cursor превысил host-bits CIDR'а.
	AllocateExternalIPv6(ctx context.Context, poolID, addressID, zoneID string) (string, error)
	// FreeExternalIPv6 — освобождает v6 у address (released_offsets ← offset;
	// addresses.external_ipv6 ← NULL). Идемпотентно.
	FreeExternalIPv6(ctx context.Context, addressID string) error

	// SetReference — атомарный CAS-upsert referrer-row + addresses.used=true.
	// Конфликт по адресу с ЧУЖИМ referrer'ом → ErrFailedPrecondition. Idempotent
	// re-attach к тому же referrer проходит.
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	// MarkEphemeralInUse — атомарно reserved=false + used=true + upsert referrer
	// (= SetReference + reset reserved).
	MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	// ClearReference удаляет referrer-row + used=false. ErrNotFound если адрес
	// не существует.
	ClearReference(ctx context.Context, addressID string) error
}
