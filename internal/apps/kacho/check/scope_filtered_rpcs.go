// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check

import (
	"sort"

	"github.com/PRO-Robotech/kacho-corelib/authz"
)

// ScopeFilteredRPCs возвращает отсортированный список полных gRPC-методов
// PermissionMap(), помеченных ScopeFiltered=true. Такой RPC не проходит per-RPC
// authz-Check (interceptor отдаёт DecisionInternal) и полагается на data-level
// list-filter для object-scope авторизации — поэтому composition root передаёт
// этот список в config.ValidateListFilter (S3 boot-guard: в production фильтр
// обязан быть включён и резолвим).
//
// Держим helper рядом с картой (пакет check), а config принимает []string —
// так config не импортирует check (нет import-цикла, Validate остаётся чистой).
func ScopeFilteredRPCs() []string {
	return ScopeFilteredRPCsOf(PermissionMap())
}

// ScopeFilteredRPCsOf — то же, но над произвольной RPCMap (тестируемость +
// переиспользование). Результат детерминирован (отсортирован).
func ScopeFilteredRPCsOf(m authz.RPCMap) []string {
	var out []string
	for full, e := range m {
		if e.ScopeFiltered {
			out = append(out, full)
		}
	}
	sort.Strings(out)
	return out
}
