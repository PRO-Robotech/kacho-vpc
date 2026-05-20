// listauthz/filter.go — KAC-127 Phase 4 generic filter helper.
//
// `FilterByAllowedIDs` — фильтрует slice [T] по allowed-ids set (Go-side).
// Используется в use-case'ах, которые не имеют отдельного `repo.ListByIDs`
// (вместо того, чтобы дублировать ListByIDs в каждом repo: SG / RT / Address /
// Gateway / PE / NIC / Subnet).
//
// Trade-off: добавляет ~O(N) cost после repo.List vs DB-level filter. Acceptable
// в типичных workload (≤1000 networks/project, page_size ≤100) — dominant cost
// — FGA-call, который cached на 5s TTL (acceptance §4.4 D-2). DB-level filter
// — оптимизация под huge lists, выполняется в `network.ListByIDs` как canonical
// reference (см. acceptance §5.2). Для остальных ресурсов VPC ~равная цена.
package listauthz

// FilterByAllowedIDs возвращает только те элементы slice, чей id входит в
// allowed-set. id извлекается через idFn (например `func(r *X) string { return r.ID }`).
// Stable order сохраняется. allowedIDs nil/empty → returns []T{} (empty, не nil).
func FilterByAllowedIDs[T any](items []T, allowedIDs []string, idFn func(T) string) []T {
	if len(items) == 0 {
		return items
	}
	if len(allowedIDs) == 0 {
		return items[:0]
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		if _, ok := allowed[idFn(item)]; ok {
			out = append(out, item)
		}
	}
	return out
}
