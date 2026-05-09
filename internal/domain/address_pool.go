package domain

import "time"

// AddressPool — internal-only resource (не выставляется через публичный VPC API).
// Содержит коллекцию CIDR-блоков, из которых аллоцируются external IPv4-адреса.
type AddressPool struct {
	ID          string // global infra resource — not bound to org/cloud/folder
	Name        string
	Description string
	Labels      map[string]string
	CIDRBlocks  []string
	Kind        AddressPoolKind
	ZoneID      string // ru-central1-a; empty = global default
	IsDefault   bool
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
	AddressPoolKindUnspecified      AddressPoolKind = 0
	AddressPoolKindExternalPublic   AddressPoolKind = 1
	AddressPoolKindExternalTest     AddressPoolKind = 2
	AddressPoolKindReservedInternal AddressPoolKind = 100
)
