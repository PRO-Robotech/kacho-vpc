package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkInterfaceFilter — фильтр для списка NIC. Wave 5 (KAC-94 D.1)
// — leaf-пакет `kacho` (parity с NetworkFilter / SecurityGroupFilter).
//
// InstanceID мапится на denorm used_by (`used_by_type='compute_instance' AND
// used_by_id=<id>`) — см. legacy `*repo.NetworkInterfaceRepo.List`. NetworkID
// игнорируется (NIC не хранит network_id; вычисляется транзитивно через
// subnet — но фильтрация по этому полю в legacy-репо тоже была no-op).
type NetworkInterfaceFilter struct {
	ProjectID   string
	InstanceID string
	SubnetID   string
	NetworkID  string
}

// NetworkInterfaceReaderIface — read-операции над NetworkInterface в TX-области.
//
// ListBySubnet нужен Subnet.Delete (precondition «нет привязанных NIC»; миграция
// `0012_nic_subnet_restrict.sql`, KAC-33 — FK ON DELETE RESTRICT, NIC жёстко
// блокирует свою подсеть). ListByInstance — fast-path фильтр для internal-tooling
// (тоже мапится на used_by).
type NetworkInterfaceReaderIface interface {
	Get(ctx context.Context, id string) (*NetworkInterfaceRecord, error)
	List(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*NetworkInterfaceRecord, string, error)
	ListBySubnet(ctx context.Context, subnetID string) ([]*NetworkInterfaceRecord, error)
}

// NetworkInterfaceWriterIface — write-операции + read (G.2 — writer видит свои
// writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance) — parity с NetworkWriterIface / SecurityGroupWriterIface.
//
// Wave 5 (KAC-94, skill evgeniy §6 G.1-G.7): NIC — самый сложный ресурс эпика
// (attach-race protection KAC-52 atomic CAS, MAC-allocation с UNIQUE-constraint,
// v4/v6 cardinality CHECK миграции 0018, ON DELETE RESTRICT каскад на Subnet,
// used_by мирроринг). Все эти инварианты — на DB-уровне (workspace CLAUDE.md
// §«Within-service refs — DB-уровень обязателен», запрет #10); writer-методы
// только маппят SQL → repo-sentinel.
type NetworkInterfaceWriterIface interface {
	NetworkInterfaceReaderIface
	// Insert вставляет NIC. MAC должен быть проставлен caller'ом (use-case
	// аллоцирует MAC через `macutil.GenerateMAC` и retry'ит на cloud-wide
	// UNIQUE-collision на mac_address). Возвращает ErrMacCollision при
	// нарушении UNIQUE на mac_address (constraint `network_interfaces_mac_address_key`)
	// — caller retry'ит с новым MAC. Прочие нарушения (folder/name UNIQUE)
	// — WrapPgErr → ErrAlreadyExists.
	Insert(ctx context.Context, n *domain.NetworkInterface) (*NetworkInterfaceRecord, error)
	// UpdateMeta мутирует name/description/labels/security_group_ids/v4_address_ids/
	// v6_address_ids. immutable: project_id/subnet_id/mac_address (handler maskcheck).
	UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*NetworkInterfaceRecord, error)
	// Delete — DELETE network_interfaces WHERE id = $1. row not affected →
	// ErrNotFound. NIC не имеет children FK, но имеет parent FK на subnets
	// (ON DELETE RESTRICT — миграция 0012, KAC-33). outbox-write — в use-case'е.
	Delete(ctx context.Context, id string) error
	// SetProjectID — не используется (NIC не поддерживает Move RPC; NIC привязан
	// к Subnet и не перемещается между folder'ами). Но iface объявляет для
	// parity с другими ресурсами — если потребуется в будущем (admin-move),
	// pg-impl уже есть.
	SetProjectID(ctx context.Context, id, folderID string) (*NetworkInterfaceRecord, error)
	// AttachToInstance — атомарный CAS на used_by_* + status=ACTIVE.
	//
	// **Race-safety (KAC-52, workspace CLAUDE.md §«Within-service refs —
	// DB-уровень обязателен», запрет #10):** single-statement conditional
	// UPDATE на одной row:
	//
	//	UPDATE network_interfaces
	//	   SET used_by_type=$2, used_by_id=$3, used_by_name=$4, status='ACTIVE'
	//	 WHERE id=$1 AND (used_by_id = '' OR used_by_id = $3)
	//	RETURNING …
	//
	// CAS-условие: либо NIC свободен (`used_by_id = ''`), либо уже attached
	// к тому же owner-у (идемпотентный re-attach). 0 rows из RETURNING →
	// ErrFailedPrecondition. Single-statement UPDATE на одной row защищён
	// row-level lock-ом Postgres: параллельный writer ждёт commit-а первого,
	// видит обновлённый row, CAS не matches → 0 rows.
	//
	// Software-side `Get → check → Update` (TOCTOU) ЗАПРЕЩЁН — этот шаблон
	// привёл к реальному инциденту 2026-05-14: две Compute.Instance.Create
	// указали один NIC, обе прошли software-guard, second writer wins.
	AttachToInstance(ctx context.Context, id, refType, refID, refName string) (*NetworkInterfaceRecord, error)
	// DetachFromInstance — idempotent UPDATE: затирает used_by_* + status=AVAILABLE.
	// Повторный detach уже-свободного NIC — no-op без error.
	DetachFromInstance(ctx context.Context, id string) (*NetworkInterfaceRecord, error)
}
