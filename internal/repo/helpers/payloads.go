// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Payload-функции возвращают JSON-snapshot record'а для outbox-payload.
// Используются CQRS-репозиториями (kacho/pg/*.go) при emit'е outbox-события в
// той же tx, что и INSERT/UPDATE/DELETE ресурса.

// RouteTablePayload — snapshot RouteTableRecord.
func RouteTablePayload(rt *kachorepo.RouteTableRecord) map[string]any {
	return DomainToMap(rt)
}

// AddressPoolDomainPayload — domain-snapshot для outbox-event.
func AddressPoolDomainPayload(p *domain.AddressPool) map[string]any {
	return DomainToMap(p)
}
