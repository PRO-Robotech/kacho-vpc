package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SubnetFilter — фильтр для списка подсетей.
//
// Wave 5 replicate (KAC-94 D.1): перенесён в leaf-пакет `kacho` (parity с
// NetworkFilter/SecurityGroupFilter), чтобы избежать import-cycle
// `repo → repo/kacho → repo`. В `internal/repo/iface.go` остался тонкий
// type-alias `SubnetFilter = kacho.SubnetFilter`.
type SubnetFilter struct {
	FolderID  string
	NetworkID string
	Name      string
	// Filter — raw filter expression (YC-syntax: `name="<value>"`).
	// Парсится в repo с whitelist allowedFields=["name"].
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
}

// SubnetWriterIface — write-операции + read (G.2 — writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance) — parity с NetworkWriterIface / SecurityGroupWriterIface.
//
// Wave 5 replicate (KAC-94): Subnet переезжает на CQRS вслед за Network/SG.
type SubnetWriterIface interface {
	SubnetReaderIface
	Insert(ctx context.Context, s *domain.Subnet) (*SubnetRecord, error)
	Update(ctx context.Context, s *domain.Subnet) (*SubnetRecord, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id у Subnet (для :move).
	SetFolderID(ctx context.Context, id, folderID string) (*SubnetRecord, error)
	// SetCidrBlocks атомарно обновляет v4_cidr_blocks и v6_cidr_blocks
	// (для AddCidrBlocks/RemoveCidrBlocks). EXCLUDE constraints
	// subnets_no_overlap_v4 / subnets_no_overlap_v6 проверяют primary CIDR
	// каждого семейства на пересечение с другими подсетями той же сети.
	SetCidrBlocks(ctx context.Context, id string, v4, v6 []string) (*SubnetRecord, error)
	// SetZoneID меняет zone_id у Subnet (для Relocate). Verbatim YC: Relocate
	// всё равно sync-FailedPrecondition'ит, метод оставлен для completeness и
	// future-state когда YC-семантика Relocate будет реализована.
	SetZoneID(ctx context.Context, id, zoneID string) (*SubnetRecord, error)
}
