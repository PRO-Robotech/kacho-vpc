// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
)

// internalMapErr — error-mapper для admin/Internal-handler'ов.
//
// Тонкая обёртка над единым leak-safe classifier'ом serviceerr.MapRepoErrLeakSafe:
// гарантирует, что raw pgx-text (несет hostname/db/query fragment) не утекает в
// response даже на cluster-internal listener (:9091) — защита от info-leak при
// ослаблении изоляции :9091 (admin-tooling, port-forward, lateral movement из
// соседнего pod). Sentinel service-errors классифицируются по коду (голый
// sentinel.Error() без wrap-tail); raw pgErr → generic Internal с `tag` без
// leak'а. Вызывается как `return internalMapErr(tag, err)`.
//
// Классификационный switch раньше жил здесь копией serviceerr.MapRepoErr /
// addresspool.mapPoolErr — консолидирован в один classifier, чтобы новый
// repo-sentinel не забыли в одной из веток (дрейф).
func internalMapErr(tag string, err error) error {
	if tag == "" {
		tag = "internal error"
	}
	return serviceerr.MapRepoErrLeakSafe(err, tag)
}
