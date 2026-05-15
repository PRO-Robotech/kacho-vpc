package repo

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/outbox"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Wave 2 batch A (KAC-94): Type-alias'ы Network/Subnet/Address/RouteTable на
// domain.*Record — объявлены в соответствующих repo-файлах. Здесь они
// доступны через package-scope. Import `domain` остаётся для SecurityGroup
// payload (он ещё не имеет Record-wrapper'а в этой итерации).

// vpcOutboxTable — имя таблицы outbox в kacho_vpc DB.
const vpcOutboxTable = "vpc_outbox"

// emitVPC — обёртка над outbox.Emit с фиксированной таблицей vpc_outbox.
// Должна вызываться внутри той же tx, что и INSERT/UPDATE/DELETE на ресурсной
// таблице (атомарность). Trigger vpc_outbox_notify_trg на каждый INSERT
// автоматически шлёт pg_notify('vpc_outbox', sequence_no::text).
//
// payload — нужно передавать произвольную map (например, snapshot domain-объекта),
// либо nil (тогда payload = `{}`). Для DELETED-event payload может содержать
// только {"id": "..."} как tombstone.
func emitVPC(ctx context.Context, tx pgx.Tx, kind, id, eventType string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	return outbox.Emit(ctx, tx, vpcOutboxTable, kind, id, eventType, payload)
}

// domainToMap конвертирует произвольный domain-объект в map[string]any через
// JSON round-trip. Используется для формирования payload outbox-события.
// При ошибке возвращает пустую map (lenient — outbox event важнее, чем
// content корректности).
func domainToMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// networkPayload — лаконичный JSON-snapshot Network (repo-entity, с CreatedAt).
// Принимает *Network (внутри repo-пакета), а не domain.Network — Wave 2 pilot
// (KAC-99/KAC-94) переместил CreatedAt из domain в repo.
func networkPayload(n *Network) map[string]any {
	return domainToMap(n)
}

// subnetPayload — snapshot Subnet (repo-entity, с CreatedAt). Wave 2 batch A
// (KAC-94) переместил CreatedAt из domain в repo.
func subnetPayload(s *Subnet) map[string]any {
	return domainToMap(s)
}

// addressPayload — snapshot Address (repo-entity, с CreatedAt).
func addressPayload(a *Address) map[string]any {
	return domainToMap(a)
}

// routeTablePayload — snapshot RouteTable (repo-entity, с CreatedAt).
func routeTablePayload(rt *RouteTable) map[string]any {
	return domainToMap(rt)
}

// securityGroupPayload — snapshot SecurityGroup.
func securityGroupPayload(sg *domain.SecurityGroup) map[string]any {
	return domainToMap(sg)
}
