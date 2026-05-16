package kacho

import "github.com/PRO-Robotech/kacho-vpc/internal/domain"

// AddressPoolRecord — repo-entity для AddressPool.
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §4 D.1 / §6 G.2 / §7 H.1):
// CQRS-entity для AddressPool — единый шаблон с NetworkRecord/AddressRecord.
//
// CreatedAt/ModifiedAt физически живут в domain.AddressPool (legacy до A.7
// эпика; integration tests конструируют `&domain.AddressPool{CreatedAt: now,
// ModifiedAt: now}` напрямую). Финальная вычистка — перенос этих полей в Record
// и удаление из domain — следующий sub-PR (за пределами sub-PR 1/6). Сейчас
// Record embed'ит domain, поля доступны через promoted-fields (`rec.CreatedAt`)
// — pg-impl читает их через тот же путь, что и legacy `*AddressPoolRepo`.
//
// Parity с `kacho.NetworkRecord` / `kacho.AddressRecord` / `kacho.SubnetRecord` —
// один Record-pattern для всех ресурсов VPC.
type AddressPoolRecord struct {
	domain.AddressPool
}
