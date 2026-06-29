// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExistenceProbe — read-only проверка существования object-scoped vpc-ресурса по
// его FGA-объекту. Используется на deny-пути authz-Check'а для existence-hiding
// (object-scoped deny на отсутствующий объект → 404 вместо 403). Легкий
// `SELECT EXISTS`, без полного чтения row.
type ExistenceProbe struct {
	pool *pgxpool.Pool
}

// NewExistenceProbe собирает probe поверх read-pool'а (обычно slave/replica, при
// ее отсутствии — master).
func NewExistenceProbe(pool *pgxpool.Pool) *ExistenceProbe {
	return &ExistenceProbe{pool: pool}
}

// objectTypeTable — whitelist FGA-тип → таблица в схеме kacho_vpc. Имя таблицы
// берется ТОЛЬКО из этой константной карты (никогда из request'а) — инъекция
// невозможна.
var objectTypeTable = map[string]string{
	"vpc_network":           "networks",
	"vpc_subnet":            "subnets",
	"vpc_address":           "addresses",
	"vpc_route_table":       "route_tables",
	"vpc_security_group":    "security_groups",
	"vpc_gateway":           "gateways",
	"vpc_network_interface": "network_interfaces",
}

// ObjectExists возвращает true, если строка с данным id есть в таблице,
// соответствующей object-типу. Неизвестный тип → ошибка (caller трактует как
// «не могу подтвердить отсутствие» → оставляет deny, fail-closed).
func (p *ExistenceProbe) ObjectExists(ctx context.Context, objectType, objectID string) (bool, error) {
	table, ok := objectTypeTable[objectType]
	if !ok {
		return false, fmt.Errorf("existence probe: unprobeable object type %q", objectType)
	}
	var exists bool
	// table — из константного whitelist выше; objectID — bound-параметр.
	if err := p.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM kacho_vpc."+table+" WHERE id = $1)", objectID,
	).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
