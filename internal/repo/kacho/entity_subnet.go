package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SubnetRecord — repo-entity для Subnet. domain.Subnet + CreatedAt (DB-managed).
//
// Wave 5 replicate (KAC-94, skill evgeniy §4 D.1 / §7 H.1): аналог NetworkRecord
// — repo-entity физически живёт рядом с repo-имплементацией (этим пакетом), не в
// domain. CreatedAt — DB-managed (проставляется в SubnetRepo.Insert через UTC-now);
// source of truth — БД.
//
// Service-слой получает *SubnetRecord из репозитория (порт `SubnetRepo` /
// SubnetReaderIface / SubnetWriterIface) и пробрасывает в DTO/handler. Через
// proto клиенту уходит CreatedAt из этой структуры (truncate до секунд —
// verbatim YC, см. `dto/type2pb/time.go`).
type SubnetRecord struct {
	domain.Subnet
	CreatedAt time.Time
}
