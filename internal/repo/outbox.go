package repo

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// KAC-94 A.7 sub-PR 4/6: реальная реализация — в `internal/repo/helpers/outbox.go`
// (`helpers.EmitVPC` / `helpers.DomainToMap` / `helpers.NetworkPayload` etc.).
// Тонкие unexported алиасы оставлены для legacy `*_repo.go`, которые будут
// удалены в Sub-PR 6.

// emitVPC — alias на helpers.EmitVPC.
func emitVPC(ctx context.Context, tx pgx.Tx, kind, id, eventType string, payload map[string]any) error {
	return helpers.EmitVPC(ctx, tx, kind, id, eventType, payload)
}

// domainToMap — alias на helpers.DomainToMap.
func domainToMap(v any) map[string]any {
	return helpers.DomainToMap(v)
}

// networkPayload — лаконичный JSON-snapshot Network (repo-entity, с CreatedAt).
func networkPayload(n *Network) map[string]any {
	return helpers.NetworkPayload(n)
}

// subnetPayload — snapshot Subnet (repo-entity, с CreatedAt).
func subnetPayload(s *Subnet) map[string]any {
	return helpers.SubnetPayload(s)
}

// addressPayload — snapshot Address (repo-entity, с CreatedAt).
func addressPayload(a *Address) map[string]any {
	return helpers.AddressPayload(a)
}

// routeTablePayload — snapshot RouteTable (repo-entity, с CreatedAt).
func routeTablePayload(rt *RouteTable) map[string]any {
	return helpers.RouteTablePayload(rt)
}

// securityGroupPayload — snapshot SecurityGroup (repo-entity, с CreatedAt).
func securityGroupPayload(sg *SecurityGroup) map[string]any {
	return helpers.SecurityGroupPayload(sg)
}

// gatewayPayload (repo-entity, с CreatedAt) объявлен в gateway_repo.go —
// чтобы не разрывать связку scan*<→>payload* по файлам.

// privateEndpointPayload (repo-entity, с CreatedAt) объявлен в private_endpoint_repo.go.
