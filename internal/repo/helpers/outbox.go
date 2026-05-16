package helpers

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/outbox"
)

// VPCOutboxTable — имя таблицы outbox в kacho_vpc DB.
const VPCOutboxTable = "vpc_outbox"

// EmitVPC — обёртка над outbox.Emit с фиксированной таблицей vpc_outbox.
// Должна вызываться внутри той же tx, что и INSERT/UPDATE/DELETE на ресурсной
// таблице (атомарность). Trigger vpc_outbox_notify_trg на каждый INSERT
// автоматически шлёт pg_notify('vpc_outbox', sequence_no::text).
//
// payload — нужно передавать произвольную map (например, snapshot domain-объекта),
// либо nil (тогда payload = `{}`). Для DELETED-event payload может содержать
// только {"id": "..."} как tombstone.
func EmitVPC(ctx context.Context, tx pgx.Tx, kind, id, eventType string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	return outbox.Emit(ctx, tx, VPCOutboxTable, kind, id, eventType, payload)
}

// DomainToMap конвертирует произвольный domain-объект в map[string]any через
// JSON round-trip. Используется для формирования payload outbox-события.
// При ошибке возвращает пустую map (lenient — outbox event важнее, чем
// content корректности).
func DomainToMap(v any) map[string]any {
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
