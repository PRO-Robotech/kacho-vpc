package domain

import "time"

// AddressPool — internal-only resource (не выставляется через публичный VPC API).
// Содержит коллекции CIDR-блоков, из которых аллоцируются external IP-адреса.
//
// KAC-71: cidr_blocks split на v4_cidr_blocks + v6_cidr_blocks (parity с Subnet);
// делает family-фильтрацию IPAM cascade явной (без runtime-парсинга каждого
// блока). Pool допустим v4-only, v6-only или dual-stack — хотя бы одно поле
// непусто (service-слой валидирует на Create/Update; миграция 0022 — defensive
// guard).
type AddressPool struct {
	ID           string // global infra resource — not bound to org/cloud/folder
	Name         string
	Description  string
	Labels       map[string]string
	V4CIDRBlocks []string // IPv4-префиксы (host-bits=0); пустой массив = pool не выдаёт v4
	V6CIDRBlocks []string // IPv6-префиксы (host-bits=0); пустой массив = pool не выдаёт v6
	Kind         AddressPoolKind
	ZoneID       string // ru-central1-a; empty = global default
	IsDefault    bool
	// SelectorLabels — whitelist labels Network'а, при котором pool участвует
	// в label-cascade-step резолва. Match-семантика: `network.pool_selector ⊆
	// pool.selector_labels`. Empty selector = pool НЕ участвует в label-cascade
	// (только через explicit binding или is_default).
	SelectorLabels   map[string]string
	SelectorPriority int32
	CreatedAt        time.Time
	ModifiedAt       time.Time
}

// AddressPoolKind — категория пула. Зеркалит enum в proto.
type AddressPoolKind int16

const (
	// AddressPoolKindUnspecified — категория пула не задана.
	AddressPoolKindUnspecified AddressPoolKind = 0
	// AddressPoolKindExternalPublic — пул публичных внешних адресов.
	AddressPoolKindExternalPublic AddressPoolKind = 1
	// KAC-70: значения 2 (EXTERNAL_TEST) и 100 (RESERVED_INTERNAL) удалены —
	// не использовались ни backend'ом, ни UI. В proto оба зарезервированы
	// (`reserved 2, 100; reserved "EXTERNAL_TEST", "RESERVED_INTERNAL";`).
)
