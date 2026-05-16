package addresspool

import (
	"context"
	"fmt"
)

// CheckUseCase — диагностика IPAM-конфигурации. Возвращает list of warnings.
// Не блокирует и не модифицирует state.
//
// Ловит ambiguous pool-конфигурации, при которых cascade-resolve выдаёт
// undefined order (множество pool с одинаковым (zone, kind, selector_labels,
// selector_priority)) — admin их подсветит и выставит distinct priority.
//
// Wave 5 A.7 sub-PR 1/6: Reader-TX kacho.Repository.
type CheckUseCase struct {
	repo Repo
}

// NewCheckUseCase собирает use-case.
func NewCheckUseCase(r Repo) *CheckUseCase {
	return &CheckUseCase{repo: r}
}

// Execute возвращает список warning-сообщений для admin'а.
// Empty zoneID = scan по всем зонам.
func (u *CheckUseCase) Execute(ctx context.Context, zoneID string) ([]string, error) {
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()

	groups, err := rd.AddressPools().FindAmbiguousSelectorGroups(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		ids := make([]string, 0, len(g))
		for _, p := range g {
			ids = append(ids, p.ID)
		}
		warnings = append(warnings, fmt.Sprintf(
			"%d pools share identical (zone_id, kind, selector_labels, selector_priority) — resolve order undefined: %v. Set distinct selector_priority to disambiguate.",
			len(g), ids,
		))
	}
	return warnings, nil
}
