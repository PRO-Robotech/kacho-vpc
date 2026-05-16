package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// KAC-94 A.7 sub-PR 4/6 — переведено на тонкие aliases на `internal/repo/helpers`.
//
// Этот файл больше не дублирует логику helpers'ов (`scanX` / `wrapXErr` /
// `payloadX` / column-list константы) — реальная реализация переехала в
// `internal/repo/helpers/`. Здесь оставлены только PascalCase-aliases для
// `internal/repo/kacho/pg/*.go` (CQRS-impl), который импортирует `repo` для
// shim'ов исторически.
//
// После Sub-PR 5/6 (переписывание integration-тестов и удаление 11 legacy
// `*_repo.go`) пакет `repo` будет удалён вместе с этим файлом; `kacho/pg`
// импортирует `helpers` напрямую.

// EmitVPC — alias на helpers.EmitVPC.
func EmitVPC(ctx context.Context, tx pgx.Tx, kind, id, eventType string, payload map[string]any) error {
	return helpers.EmitVPC(ctx, tx, kind, id, eventType, payload)
}

// WrapPgErr — alias на helpers.WrapPgErr.
func WrapPgErr(err error, kind, id string) error {
	return helpers.WrapPgErr(err, kind, id)
}

// IsFKViolation — alias на helpers.IsFKViolation.
func IsFKViolation(err error) bool {
	return helpers.IsFKViolation(err)
}

// MarshalJSONB — alias на helpers.MarshalJSONB.
func MarshalJSONB(v any, field string) ([]byte, error) {
	return helpers.MarshalJSONB(v, field)
}

// UnmarshalJSONB — alias на helpers.UnmarshalJSONB.
func UnmarshalJSONB(raw []byte, target any, field string) error {
	return helpers.UnmarshalJSONB(raw, target, field)
}

// EncodePageToken — alias на helpers.EncodePageToken.
func EncodePageToken(createdAt time.Time, id string) string {
	return helpers.EncodePageToken(createdAt, id)
}

// DecodePageToken — alias на helpers.DecodePageToken.
func DecodePageToken(token string) (time.Time, string, error) {
	return helpers.DecodePageToken(token)
}

// InvalidPageTokenErr — alias на helpers.InvalidPageTokenErr.
func InvalidPageTokenErr(err error) error {
	return helpers.InvalidPageTokenErr(err)
}

// InvalidFilterErr — alias на helpers.InvalidFilterErr.
func InvalidFilterErr(err error) error {
	return helpers.InvalidFilterErr(err)
}

// NetworkPayload — alias на helpers.NetworkPayload.
func NetworkPayload(n *kachorepo.NetworkRecord) map[string]any {
	return helpers.NetworkPayload(n)
}

// NetworkCols — alias на helpers.NetworkCols.
const NetworkCols = helpers.NetworkCols

// Scannable — alias for helpers.Scannable, чтобы kacho/pg мог типизировать row.
type Scannable = helpers.Scannable

// ScanNetwork — alias на helpers.ScanNetwork.
func ScanNetwork(row Scannable) (*kachorepo.NetworkRecord, error) {
	return helpers.ScanNetwork(row)
}

// ---- SecurityGroup shims ----

// SGCols — alias на helpers.SGCols.
const SGCols = helpers.SGCols

// ScanSG — alias на helpers.ScanSG.
func ScanSG(row Scannable) (*kachorepo.SecurityGroupRecord, error) {
	return helpers.ScanSG(row)
}

// WrapSGErr — alias на helpers.WrapSGErr (verbatim-YC SG not-found text).
func WrapSGErr(err error, id string) error {
	return helpers.WrapSGErr(err, id)
}

// SecurityGroupPayload — alias на helpers.SecurityGroupPayload.
func SecurityGroupPayload(sg *kachorepo.SecurityGroupRecord) map[string]any {
	return helpers.SecurityGroupPayload(sg)
}

// NullableStr — alias на helpers.NullableStr.
func NullableStr(s string) *string {
	return helpers.NullableStr(s)
}

// ---- RouteTable shims ----

// RouteTableCols — alias на helpers.RouteTableCols.
const RouteTableCols = helpers.RouteTableCols

// ScanRouteTable — alias на helpers.ScanRouteTable.
func ScanRouteTable(row Scannable) (*kachorepo.RouteTableRecord, error) {
	return helpers.ScanRouteTable(row)
}

// MarshalStaticRoutes — alias на helpers.MarshalStaticRoutes.
func MarshalStaticRoutes(routes []domain.StaticRoute) ([]byte, error) {
	return helpers.MarshalStaticRoutes(routes)
}

// RouteTablePayload — alias на helpers.RouteTablePayload.
func RouteTablePayload(rt *kachorepo.RouteTableRecord) map[string]any {
	return helpers.RouteTablePayload(rt)
}

// ---- PrivateEndpoint shims ----

// PECols — alias на helpers.PECols.
const PECols = helpers.PECols

// ScanPrivateEndpoint — alias на helpers.ScanPrivateEndpoint.
func ScanPrivateEndpoint(row Scannable) (*kachorepo.PrivateEndpointRecord, error) {
	return helpers.ScanPrivateEndpoint(row)
}

// PrivateEndpointPayload — alias на helpers.PrivateEndpointPayload.
func PrivateEndpointPayload(pe *kachorepo.PrivateEndpointRecord) map[string]any {
	return helpers.PrivateEndpointPayload(pe)
}

// ---- Address shims ----

// AddressCols — alias на helpers.AddressCols.
const AddressCols = helpers.AddressCols

// ScanAddress — alias на helpers.ScanAddress.
func ScanAddress(row Scannable) (*kachorepo.AddressRecord, error) {
	return helpers.ScanAddress(row)
}

// AddressPayload — alias на helpers.AddressPayload.
func AddressPayload(a *kachorepo.AddressRecord) map[string]any {
	return helpers.AddressPayload(a)
}

// AllocateFromFreelistSQL — alias на helpers.AllocateFromFreelistSQL.
const AllocateFromFreelistSQL = helpers.AllocateFromFreelistSQL

// ---- Subnet shims ----

// SubnetCols — alias на helpers.SubnetCols.
const SubnetCols = helpers.SubnetCols

// ScanSubnet — alias на helpers.ScanSubnet.
func ScanSubnet(row Scannable) (*kachorepo.SubnetRecord, error) {
	return helpers.ScanSubnet(row)
}

// SubnetPayload — alias на helpers.SubnetPayload.
func SubnetPayload(s *kachorepo.SubnetRecord) map[string]any {
	return helpers.SubnetPayload(s)
}

// IsExclusionViolation — alias на helpers.IsExclusionViolation.
func IsExclusionViolation(err error) bool {
	return helpers.IsExclusionViolation(err)
}

// MarshalDhcp — alias на helpers.MarshalDhcp.
func MarshalDhcp(d *domain.DhcpOptions) ([]byte, error) {
	return helpers.MarshalDhcp(d)
}

// ---- Gateway shims ----

// GatewayCols — alias на helpers.GatewayCols.
const GatewayCols = helpers.GatewayCols

// ScanGateway — alias на helpers.ScanGateway.
func ScanGateway(row Scannable) (*kachorepo.GatewayRecord, error) {
	return helpers.ScanGateway(row)
}

// WrapGatewayErr — alias на helpers.WrapGatewayErr.
func WrapGatewayErr(err error, id string) error {
	return helpers.WrapGatewayErr(err, id)
}

// GatewayPayload — alias на helpers.GatewayPayload.
func GatewayPayload(g *kachorepo.GatewayRecord) map[string]any {
	return helpers.GatewayPayload(g)
}

// ---- AddressPool shims ----

// AddressPoolCols — alias на helpers.AddressPoolCols.
const AddressPoolCols = helpers.AddressPoolCols

// ScanAddressPool — alias на helpers.ScanAddressPool.
// Принимает pgx.Row (а не Scannable interface — parity с pgxpool Repo API).
func ScanAddressPool(row pgx.Row) (*kachorepo.AddressPoolRecord, error) {
	dom, err := helpers.ScanAddressPool(row)
	if err != nil {
		return nil, err
	}
	return &kachorepo.AddressPoolRecord{AddressPool: *dom}, nil
}

// AddressPoolDomainPayload — alias на helpers.AddressPoolDomainPayload.
func AddressPoolDomainPayload(p *domain.AddressPool) map[string]any {
	return helpers.AddressPoolDomainPayload(p)
}

// AddressPoolPayload — alias на helpers.AddressPoolPayload.
func AddressPoolPayload(rec *kachorepo.AddressPoolRecord) map[string]any {
	return helpers.AddressPoolPayload(rec)
}

// ---- AddressPoolBinding / CloudPoolSelector — нет shim'ов: SQL inline в pg-impl.

// JoinAnd — alias на helpers.JoinAnd.
func JoinAnd(conds []string) string {
	return helpers.JoinAnd(conds)
}

// NormalizeMap — alias на helpers.NormalizeMap.
func NormalizeMap(m map[string]string) map[string]string {
	return helpers.NormalizeMap(m)
}
