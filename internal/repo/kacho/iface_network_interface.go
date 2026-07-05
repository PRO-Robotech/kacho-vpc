// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkInterfaceFilter — фильтр для списка NIC. Живет в leaf-пакете `kacho`,
// как NetworkFilter / SecurityGroupFilter.
//
// InstanceID мапится на denorm used_by (`used_by_type='compute_instance' AND
// used_by_id=<id>`). NetworkID игнорируется: NIC не хранит network_id (он
// вычисляется транзитивно через subnet), фильтрация по этому полю — no-op.
type NetworkInterfaceFilter struct {
	ProjectID  string
	InstanceID string
	SubnetID   string
	NetworkID  string
}

// NetworkInterfaceReaderIface — read-операции над NetworkInterface в TX-области.
//
// ListBySubnet нужен Subnet.Delete (precondition «нет привязанных NIC»: FK
// ON DELETE RESTRICT, NIC жестко блокирует свою подсеть). ListByInstance —
// fast-path фильтр для internal-tooling (тоже мапится на used_by).
type NetworkInterfaceReaderIface interface {
	Get(ctx context.Context, id string) (*NetworkInterfaceRecord, error)
	List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*NetworkInterfaceRecord, string, error)
	ListBySubnet(ctx context.Context, subnetID string) ([]*NetworkInterfaceRecord, error)
	// ListByIDs — per-object filtered List (`WHERE id = ANY`), pagination после
	// фильтра. Пустой allowedIDs → (nil, "", nil).
	ListByIDs(ctx context.Context, f NetworkInterfaceFilter, allowedIDs []string, p Pagination) ([]*NetworkInterfaceRecord, string, error)
}

// NetworkInterfaceWriterIface — write-операции плюс read (writer видит свои
// writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Атомарность DML + outbox держится на том, что обе операции идут через одну
// pgx.Tx (writer-instance), как у NetworkWriterIface / SecurityGroupWriterIface.
//
// NIC — самый сложный ресурс домена: MAC-аллокация с UNIQUE-constraint, v4/v6
// cardinality CHECK, ON DELETE RESTRICT на Subnet, used_by-мирроринг. Все эти
// инварианты держатся на DB-уровне (ban #10); writer-методы лишь маппят SQL →
// repo-sentinel.
type NetworkInterfaceWriterIface interface {
	NetworkInterfaceReaderIface
	// Insert вставляет NIC. MAC проставляет caller (use-case аллоцирует его
	// через `macutil.GenerateMAC` и retry'ит на cloud-wide UNIQUE-collision по
	// mac_address). При нарушении UNIQUE на mac_address (constraint
	// `network_interfaces_mac_address_key`) возвращает ErrMacCollision — caller
	// retry'ит с новым MAC. Прочие нарушения (project/name UNIQUE) — WrapPgErr →
	// ErrAlreadyExists.
	Insert(ctx context.Context, n *domain.NetworkInterface) (*NetworkInterfaceRecord, error)
	// UpdateMeta мутирует name/description/labels/security_group_ids/v4_address_ids/
	// v6_address_ids. immutable: project_id/subnet_id/mac_address (handler maskcheck).
	UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*NetworkInterfaceRecord, error)
	// GetForUpdate — Get с `SELECT ... FOR UPDATE` (row-lock) внутри writer-TX.
	// Сериализует read-modify-write в Update (doUpdate): конкурентный Update
	// блокируется на GetForUpdate до commit первого, затем читает уже обновлённый
	// row и применяет свою маску поверх — lost-update mutable-колонок NIC исключён
	// (project-rule #10; address-ref side уже защищён SetReference-CAS).
	GetForUpdate(ctx context.Context, id string) (*NetworkInterfaceRecord, error)
	// Delete — DELETE network_interfaces WHERE id = $1; row не затронут →
	// ErrNotFound. NIC не имеет children FK, но имеет parent FK на subnets
	// (ON DELETE RESTRICT). outbox-write — в use-case'е.
	Delete(ctx context.Context, id string) error
}
