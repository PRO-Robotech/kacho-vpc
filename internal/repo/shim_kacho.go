package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Shim для пакета `internal/repo/kacho/pg`: экспортирует helper'ы, которые
// раньше были unexported в `repo` (scanNetwork / wrapPgErr / marshal/unmarshal
// JSONB / page-token / FK-detection / emitVPC / networkCols).
//
// Wave 5 (KAC-94, skill evgeniy §6 G.1-G.7): CQRS-impl `kacho/pg` живёт в
// отдельном пакете и не может видеть unexported-имена `repo`. Чтобы не
// дублировать ~200 строк помощников — экспортируем нужный subset наружу
// через camelCase→PascalCase-aliases. Legacy-код в этом же пакете продолжает
// пользоваться оригинальными unexported-именами.
//
// Альтернатива (отдельный shared-helper-package) — крупный рефакторинг,
// выходит за scope pilot'а. Эти shim'ы — узкий surface; при replicate-фазе
// на 7 ресурсов добавим экспорты для их scan-функций / payload-helper'ов.

// EmitVPC — exported alias of emitVPC; used by kacho/pg/repository.go.
func EmitVPC(ctx context.Context, tx pgx.Tx, kind, id, eventType string, payload map[string]any) error {
	return emitVPC(ctx, tx, kind, id, eventType, payload)
}

// WrapPgErr — exported alias of wrapPgErr; used by kacho/pg/network.go.
func WrapPgErr(err error, kind, id string) error {
	return wrapPgErr(err, kind, id)
}

// IsFKViolation — exported alias of isFKViolation; used by kacho/pg/network.go.
func IsFKViolation(err error) bool {
	return isFKViolation(err)
}

// MarshalJSONB — exported alias of marshalJSONB; used by kacho/pg/network.go.
func MarshalJSONB(v any, field string) ([]byte, error) {
	return marshalJSONB(v, field)
}

// UnmarshalJSONB — exported alias of unmarshalJSONB; used by kacho/pg/network.go.
func UnmarshalJSONB(raw []byte, target any, field string) error {
	return unmarshalJSONB(raw, target, field)
}

// EncodePageToken — exported alias of encodePageToken; used by kacho/pg/network.go.
func EncodePageToken(createdAt time.Time, id string) string {
	return encodePageToken(createdAt, id)
}

// DecodePageToken — exported alias of decodePageToken; used by kacho/pg/network.go.
func DecodePageToken(token string) (time.Time, string, error) {
	return decodePageToken(token)
}

// InvalidPageTokenErr — exported alias; used by kacho/pg/network.go.
func InvalidPageTokenErr(err error) error {
	return invalidPageTokenErr(err)
}

// InvalidFilterErr — exported alias; used by kacho/pg/network.go.
func InvalidFilterErr(err error) error {
	return invalidFilterErr(err)
}

// NetworkPayload — exported alias of networkPayload (для outbox-snapshot).
func NetworkPayload(n *kachorepo.NetworkRecord) map[string]any {
	return networkPayload(n)
}

// NetworkCols — exported network column list; используется kacho/pg/network.go
// в SQL-запросах.
const NetworkCols = networkCols

// Scannable — alias for scannable, чтобы kacho/pg мог типизировать row.
type Scannable = scannable

// ScanNetwork — exported alias of scanNetwork.
func ScanNetwork(row Scannable) (*kachorepo.NetworkRecord, error) {
	return scanNetwork(row)
}

// ---- SecurityGroup shims (Wave 5 batch 33/34, KAC-94: SG → CQRS-репо) ----

// SGCols — exported security_groups column list; used by kacho/pg/security_group.go.
const SGCols = sgCols

// ScanSG — exported alias of scanSG; used by kacho/pg/security_group.go.
func ScanSG(row Scannable) (*domain.SecurityGroupRecord, error) {
	return scanSG(row)
}

// WrapSGErr — exported alias of wrapSGErr (verbatim-YC SG not-found text);
// used by kacho/pg/security_group.go.
func WrapSGErr(err error, id string) error {
	return wrapSGErr(err, id)
}

// SecurityGroupPayload — exported alias of securityGroupPayload (outbox snapshot).
func SecurityGroupPayload(sg *domain.SecurityGroupRecord) map[string]any {
	return securityGroupPayload(sg)
}

// NullableStr — exported alias of nullableStr ("" → SQL NULL). Used by SG-pg-impl
// to keep network_id NULL-able (SG can be unbound / folder-level).
func NullableStr(s string) *string {
	return nullableStr(s)
}
