// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

import "time"

// AddressPool — internal-only resource (не выставляется через публичный VPC API).
// Содержит коллекции CIDR-блоков, из которых аллоцируются external IP-адреса.
//
// CIDR-блоки разделены по family — v4_cidr_blocks + v6_cidr_blocks (parity с
// Subnet); это делает family-фильтрацию IPAM cascade явной (без runtime-парсинга
// каждого блока). Pool допустим v4-only, v6-only или dual-stack — хотя бы одно
// поле непусто (service-слой валидирует на Create/Update, на DB-уровне —
// defensive guard).
type AddressPool struct {
	ID           string // global infra resource — не привязан к project
	Name         string
	Description  string
	Labels       map[string]string
	V4CIDRBlocks []string // IPv4-префиксы (host-bits=0); пустой массив = pool не выдает v4
	V6CIDRBlocks []string // IPv6-префиксы (host-bits=0); пустой массив = pool не выдает v6
	Kind         AddressPoolKind
	ZoneID       string // id зоны; empty = global default
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
	AddressPoolKindUnspecified    AddressPoolKind = 0
	AddressPoolKindExternalPublic AddressPoolKind = 1
)
