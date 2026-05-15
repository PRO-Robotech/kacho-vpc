// Package service — оставшаяся часть NIC API в `internal/service`.
//
// Wave 3 (KAC-94): NetworkInterfaceService переехал в
// `internal/apps/kacho/api/networkinterface` как набор use-case'ов (skill evgeniy
// §2 B.1-B.4). Здесь остаются:
//
//   - `NetworkInterfaceFilter` — value-type фильтра List; репо реализует именно
//     этот shape, поэтому он должен оставаться в одном leaf-пакете. Use-case-
//     пакет `networkinterface` экспортирует его type-alias через
//     `NetworkInterfaceFilter = service.NetworkInterfaceFilter`.
//   - `NetworkInterfaceRepo` — НАРРОВЫЙ port-интерфейс «только то, что нужно
//     `SubnetService.Delete` precondition-проверке» (FK RESTRICT NIC→Subnet,
//     миграция 0012, KAC-33). Полный repo-интерфейс — в use-case-пакете
//     `internal/apps/kacho/api/networkinterface/ports.go`.
//
// Старый `NetworkInterfaceService` / public CRUD + Attach/Detach use-case'ы
// удалены — они теперь живут в `internal/apps/kacho/api/networkinterface/`.
// Старый handler `internal/handler/network_interface_handler.go` тоже удалён —
// его роль выполняет `(*networkinterface.Handler)`.
package service

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// niReferrerType — `ReferrerType` в `address_references` для адресов, привязанных
// к NIC. Дублирует константу `internal/apps/kacho/api/networkinterface.niReferrerType`
// (use-case-пакет несёт свою копию, чтобы не зависеть от service-leaf'а; здесь
// та же константа нужна в `AddressService` для дешёвой проверки «адрес занят
// NIC'ом» при Delete-precondition).
const niReferrerType = "network_interface"

// NetworkInterfaceFilter — фильтр для List. Используется repo-уровнем; полный
// CRUD-интерфейс репозитория NIC живёт в `internal/apps/kacho/api/networkinterface`.
type NetworkInterfaceFilter struct {
	FolderID   string
	InstanceID string
	SubnetID   string
	// NetworkID — не поддерживается фильтром (NIC не хранит network_id), поле
	// оставлено для совместимости с handler-сигнатурой; репо его игнорирует.
	NetworkID string
}

// NetworkInterfaceRepo — НАРРОВЫЙ port-интерфейс, нужный `SubnetService.Delete`
// для precondition «нет ли в подсети NIC, приаттачено к инстансу». Полный
// repo-интерфейс (Get/List/Insert/UpdateMeta/SetUsedBy/Delete) — в
// `internal/apps/kacho/api/networkinterface/ports.go::NetworkInterfaceRepo`.
//
// Адаптер `internal/repo.NetworkInterfaceRepo` реализует оба интерфейса (они
// независимы), потому что метод-набор обоих ⊆ публичной поверхности repo-типа.
type NetworkInterfaceRepo interface {
	// ListBySubnet возвращает все NIC, привязанные к указанной подсети (без
	// пагинации). Используется как sync-precondition в Subnet.Delete.
	ListBySubnet(ctx context.Context, subnetID string) ([]*domain.NetworkInterfaceRecord, error)
}
