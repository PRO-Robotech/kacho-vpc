package domain

import "time"

// CloudPoolSelector — admin-controlled routing-labels конкретного Cloud.
// Используется в IPAM cascade при AllocateExternalIP. Хранится в internal-only
// таблице cloud_pool_selector (миграция 0022).
//
// Не путать с Cloud.labels (public field в kacho-resource-manager) —
// CloudPoolSelector это admin-системный pool routing, а не пользовательские
// labels для поиска ресурса.
type CloudPoolSelector struct {
	CloudID  string
	Selector map[string]string
	SetAt    time.Time
	SetBy    string
}

// IsEmpty — нет ни одной метки.
func (c CloudPoolSelector) IsEmpty() bool { return len(c.Selector) == 0 }
