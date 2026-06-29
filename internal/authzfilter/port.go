// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package authzfilter

import "context"

// UseCasePort — форма per-object фильтра, которую видит use-case-слой (clean-arch:
// use-case определяет узкий порт `ListFilter` с этой же сигнатурой и не зависит
// от Decision/AuthorizeClient transport-деталей). Возврат:
//   - allowedIDs: explicit set разрешенных id (передается в repo.ListByIDs).
//   - bypass:     true → фильтр не сужает (wildcard scope_grant / disabled);
//     use-case делает обычный repo.List (все project-scoped строки).
//   - err:        infra-ошибка fail-closed (Unavailable) — use-case пробрасывает.
//
// bypass=false && len(allowedIDs)==0 → пустой результат (no-leak).
type UseCasePort interface {
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (allowedIDs []string, bypass bool, err error)
}

// portAdapter адаптирует Filter (Decision-возврат) к UseCasePort.
type portAdapter struct{ f Filter }

// AsPort оборачивает Filter в UseCasePort. f == nil → nil (use-case трактует
// nil-порт как passthrough — dev / list-filter disabled).
func AsPort(f Filter) UseCasePort {
	if f == nil {
		return nil
	}
	// Typed-nil guard: *FGAFilter(nil) переданный как Filter — тоже passthrough.
	if ff, ok := f.(*FGAFilter); ok && ff == nil {
		return nil
	}
	return &portAdapter{f: f}
}

func (a *portAdapter) ListAllowedIDs(ctx context.Context, subject, resourceType, action string) ([]string, bool, error) {
	d, err := a.f.ListAllowedIDs(ctx, subject, resourceType, action)
	if err != nil {
		return nil, false, err
	}
	if d.BypassAll {
		return nil, true, nil
	}
	return d.AllowedIDs, false, nil
}

// EnforceVisible — per-object no-leak enforce для Get. Возвращает:
//   - (true,  nil)  — объект id входит в accessible-set (или bypass / passthrough);
//     caller отдает ресурс.
//   - (false, nil)  — объект НЕ виден; caller обязан вернуть NotFound (no-leak —
//     тот же текст, что и несуществующий ресурс; НЕ PermissionDenied).
//   - (false, err)  — fail-closed infra-ошибка (Unavailable); caller пробрасывает.
//
// port == nil или subject == "" → (true, nil) passthrough (enforce делает
// per-RPC interceptor в dev / для system-principal).
func EnforceVisible(ctx context.Context, port UseCasePort, subject, resourceType, action, id string) (bool, error) {
	if port == nil || subject == "" {
		return true, nil
	}
	allowedIDs, bypass, err := port.ListAllowedIDs(ctx, subject, resourceType, action)
	if err != nil {
		return false, err
	}
	if bypass {
		return true, nil
	}
	for _, a := range allowedIDs {
		if a == id {
			return true, nil
		}
	}
	return false, nil
}
