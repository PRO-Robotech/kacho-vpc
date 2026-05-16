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
func ScanSG(row Scannable) (*kachorepo.SecurityGroupRecord, error) {
	return scanSG(row)
}

// WrapSGErr — exported alias of wrapSGErr (verbatim-YC SG not-found text);
// used by kacho/pg/security_group.go.
func WrapSGErr(err error, id string) error {
	return wrapSGErr(err, id)
}

// SecurityGroupPayload — exported alias of securityGroupPayload (outbox snapshot).
func SecurityGroupPayload(sg *kachorepo.SecurityGroupRecord) map[string]any {
	return securityGroupPayload(sg)
}

// NullableStr — exported alias of nullableStr ("" → SQL NULL). Used by SG-pg-impl
// to keep network_id NULL-able (SG can be unbound / folder-level).
func NullableStr(s string) *string {
	return nullableStr(s)
}

// ---- RouteTable shims (Wave 5 replicate, KAC-94: RT → CQRS-репо) ----

// RouteTableCols — exported route_tables column list; used by kacho/pg/route_table.go.
const RouteTableCols = routeTableCols

// ScanRouteTable — exported alias of scanRouteTable; used by kacho/pg/route_table.go.
func ScanRouteTable(row Scannable) (*kachorepo.RouteTableRecord, error) {
	return scanRouteTable(row)
}

// MarshalStaticRoutes — exported alias of marshalStaticRoutes; used by
// kacho/pg/route_table.go для подготовки jsonb-payload `static_routes`.
func MarshalStaticRoutes(routes []domain.StaticRoute) ([]byte, error) {
	return marshalStaticRoutes(routes)
}

// RouteTablePayload — exported alias of routeTablePayload (outbox snapshot).
// Используется apps/kacho/api/routetable/helpers.go при emit'е outbox-событий из
// use-case-кода.
func RouteTablePayload(rt *kachorepo.RouteTableRecord) map[string]any {
	return routeTablePayload(rt)
}

// ---- PrivateEndpoint shims (Wave 5 replicate, KAC-94: PE → CQRS-репо) ----

// PECols — exported private_endpoints column list; used by kacho/pg/private_endpoint.go.
const PECols = peCols

// ScanPrivateEndpoint — exported alias of scanPrivateEndpoint; used by
// kacho/pg/private_endpoint.go.
func ScanPrivateEndpoint(row Scannable) (*kachorepo.PrivateEndpointRecord, error) {
	return scanPrivateEndpoint(row)
}

// PrivateEndpointPayload — exported alias of privateEndpointPayload (outbox
// snapshot). Используется apps/kacho/api/privateendpoint/helpers.go при emit'е
// outbox-событий из use-case-кода.
func PrivateEndpointPayload(pe *kachorepo.PrivateEndpointRecord) map[string]any {
	return privateEndpointPayload(pe)
}

// ---- Address shims (Wave 5 replicate, KAC-94: Address → CQRS-репо) ----

// AddressCols — exported addresses column list; used by kacho/pg/address.go
// в SQL-запросах. Parity с NetworkCols / SGCols / RouteTableCols.
const AddressCols = addressCols

// ScanAddress — exported alias of scanAddress; used by kacho/pg/address.go.
func ScanAddress(row Scannable) (*kachorepo.AddressRecord, error) {
	return scanAddress(row)
}

// AddressPayload — exported alias of addressPayload (outbox snapshot).
// Используется apps/kacho/api/address/helpers.go при emit'е outbox-событий из
// use-case-кода.
func AddressPayload(a *kachorepo.AddressRecord) map[string]any {
	return addressPayload(a)
}

// AllocateFromFreelistSQL — exported SQL для PG-native v4 freelist allocator;
// используется kacho/pg/address.go::AllocateIPFromFreelist (атомарный pop из
// address_pool_free_ips FOR UPDATE SKIP LOCKED + UPDATE addresses.external_ipv4
// — один SQL-statement, нулевая contention между параллельными аллокаторами).
const AllocateFromFreelistSQL = allocateFromFreelistSQL
