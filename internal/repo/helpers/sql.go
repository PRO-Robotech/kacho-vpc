package helpers

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// JoinAnd соединяет conds через " AND " для построения композитного WHERE.
// Empty slice → "".
func JoinAnd(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

// NullableStr: "" → nil (SQL NULL); non-empty → &s. Используется для optional
// колонок (например, SecurityGroup.network_id — SG может быть folder-level).
func NullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// NormalizeMap: nil → empty map (deterministic JSONB). Используется для
// admin-controlled label-selector'ов (cloud_pool_selector).
func NormalizeMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// MarshalDhcp — JSONB-сериализация Subnet.dhcp_options. nil → nil (NULL в SQL),
// non-nil → JSONB bytes.
func MarshalDhcp(d *domain.DhcpOptions) ([]byte, error) {
	if d == nil {
		return nil, nil
	}
	return MarshalJSONB(d, "Subnet.dhcp_options")
}

// MarshalStaticRoutes — JSONB-сериализация RouteTable.static_routes. nil → "[]",
// non-nil → JSONB bytes (empty array вместо null — для deterministic UPSERT).
func MarshalStaticRoutes(routes []domain.StaticRoute) ([]byte, error) {
	if routes == nil {
		return []byte("[]"), nil
	}
	return MarshalJSONB(routes, "RouteTable.static_routes")
}
