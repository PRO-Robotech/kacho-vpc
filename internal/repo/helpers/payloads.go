package helpers

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Все payload-функции возвращают JSON-snapshot record'а для outbox-payload.
// Используются репозиториями (legacy `*_repo.go` и CQRS `kacho/pg/*.go`) при
// emit'е outbox-события в той же tx, что и INSERT/UPDATE/DELETE ресурса.

// NetworkPayload — лаконичный JSON-snapshot NetworkRecord (с CreatedAt).
func NetworkPayload(n *kachorepo.NetworkRecord) map[string]any {
	return DomainToMap(n)
}

// SubnetPayload — snapshot SubnetRecord.
func SubnetPayload(s *kachorepo.SubnetRecord) map[string]any {
	return DomainToMap(s)
}

// AddressPayload — snapshot AddressRecord.
func AddressPayload(a *kachorepo.AddressRecord) map[string]any {
	return DomainToMap(a)
}

// RouteTablePayload — snapshot RouteTableRecord.
func RouteTablePayload(rt *kachorepo.RouteTableRecord) map[string]any {
	return DomainToMap(rt)
}

// SecurityGroupPayload — snapshot SecurityGroupRecord.
func SecurityGroupPayload(sg *kachorepo.SecurityGroupRecord) map[string]any {
	return DomainToMap(sg)
}

// GatewayPayload — snapshot GatewayRecord.
func GatewayPayload(g *kachorepo.GatewayRecord) map[string]any {
	return DomainToMap(g)
}

// PrivateEndpointPayload — snapshot PrivateEndpointRecord.
func PrivateEndpointPayload(pe *kachorepo.PrivateEndpointRecord) map[string]any {
	return DomainToMap(pe)
}

// NetworkInterfacePayload — snapshot NetworkInterfaceRecord.
func NetworkInterfacePayload(rec *kachorepo.NetworkInterfaceRecord) map[string]any {
	return DomainToMap(rec)
}

// AddressPoolDomainPayload — domain-snapshot для outbox-event.
func AddressPoolDomainPayload(p *domain.AddressPool) map[string]any {
	return DomainToMap(p)
}

// AddressPoolPayload — outbox snapshot для Record-payload (parity с
// NetworkPayload / AddressPayload). Делегирует на domain-payload через embed.
func AddressPoolPayload(rec *kachorepo.AddressPoolRecord) map[string]any {
	if rec == nil {
		return nil
	}
	return DomainToMap(&rec.AddressPool)
}
