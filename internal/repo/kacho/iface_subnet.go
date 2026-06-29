// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SubnetFilter — фильтр для списка подсетей. Живет в leaf-пакете `kacho`
// (как NetworkFilter/SecurityGroupFilter), чтобы избежать import-cycle
// `repo → repo/kacho → repo`; в `internal/repo/iface.go` — тонкий type-alias
// `SubnetFilter = kacho.SubnetFilter`.
type SubnetFilter struct {
	ProjectID string
	NetworkID string
	Name      string
	// Filter — сырое выражение фильтра (`name="<value>"`); парсится в repo с
	// whitelist allowedFields=["name"].
	Filter string
}

// SubnetReaderIface — read-операции над Subnet в read-only TX-области.
type SubnetReaderIface interface {
	Get(ctx context.Context, id string) (*SubnetRecord, error)
	List(ctx context.Context, f SubnetFilter, p Pagination) ([]*SubnetRecord, string, error)
	// AddressesBySubnet возвращает Address-ресурсы, привязанные к подсети
	// (через internal_ipv4.subnet_id ИЛИ internal_ipv6.subnet_id).
	// Используется ListUsedAddresses и SubnetService.Delete (sync precheck).
	AddressesBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*AddressRecord, string, error)
	// ListByIDs возвращает только subnets с id из allowedIDs
	// (`WHERE id = ANY($allowed)`); pagination применяется к отфильтрованному
	// набору. Пустой allowedIDs → (nil, "", nil) short-circuit. Используется в
	// per-object filtered List.
	ListByIDs(ctx context.Context, f SubnetFilter, allowedIDs []string, p Pagination) ([]*SubnetRecord, string, error)
}

// SubnetWriterIface — write-операции плюс read (writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Атомарность DML + outbox держится на том, что обе операции идут через одну
// pgx.Tx (writer-instance), как у NetworkWriterIface / SecurityGroupWriterIface.
type SubnetWriterIface interface {
	SubnetReaderIface
	Insert(ctx context.Context, s *domain.Subnet) (*SubnetRecord, error)
	Update(ctx context.Context, s *domain.Subnet) (*SubnetRecord, error)
	Delete(ctx context.Context, id string) error
	// SetCidrBlocks атомарно обновляет v4_cidr_blocks и v6_cidr_blocks
	// (для AddCidrBlocks/RemoveCidrBlocks). EXCLUDE constraints
	// subnets_no_overlap_v4 / subnets_no_overlap_v6 проверяют primary CIDR
	// каждого семейства на пересечение с другими подсетями той же сети.
	SetCidrBlocks(ctx context.Context, id string, v4, v6 []string) (*SubnetRecord, error)
	// GetForUpdate — Get с `SELECT ... FOR UPDATE` (row-lock) внутри writer-TX.
	// Сериализует конкурентные read-modify-write над v*_cidr_blocks
	// (AddCidrBlocks/RemoveCidrBlocks): без него два параллельных запроса читали
	// бы один snapshot и второй commit затирал бы изменения первого (lost-update).
	GetForUpdate(ctx context.Context, id string) (*SubnetRecord, error)
}
