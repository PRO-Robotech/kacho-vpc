// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package addresspool — table-driven unit-тест mapping'а `Handler.Update`.
//
// UpdateAddressPoolRequest несет per-field флаги (replace_labels / update_is_default /
// replace_selector_labels / update_selector_priority; name/description — по непустому
// значению). Тест фиксирует контракт тонкого transport-слоя: какой набор флагов
// request'а → какие поля мутируются, а какие остаются нетронутыми.
//
// Прогоняется реальный UpdateAddressPoolUseCase поверх kachomock.Repository:
// seed pool → h.Update(req) → проверка наблюдаемого эффекта. Поле, флаг которого
// НЕ выставлен, остается неизменным.
package addresspool

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// updateHandlerFixture — Handler с реальным UpdateAddressPoolUseCase поверх
// kachomock; остальные use-case'ы не нужны (тест зовет только Update).
type updateHandlerFixture struct {
	h  *Handler
	kr *kachomock.Repository
}

func newUpdateHandlerFixture(t *testing.T) *updateHandlerFixture {
	t.Helper()
	kr := kachomock.NewRepository()
	h := NewHandler(
		nil,                             // create
		NewUpdateAddressPoolUseCase(kr), // update — under-test
		nil,                             // delete
		NewGetAddressPoolUseCase(kr),    // get — no-op short-circuit (пустой набор флагов)
		nil, nil, nil, nil, nil, nil, nil,
	)
	return &updateHandlerFixture{h: h, kr: kr}
}

// seed — кладет pool в начальном состоянии (минуя use-case).
func (f *updateHandlerFixture) seed(t *testing.T, p *domain.AddressPool) {
	t.Helper()
	now := time.Now().UTC()
	if p.ID == "" {
		p.ID = ids.NewID(ids.PrefixAddressPool)
	}
	p.CreatedAt = now
	p.ModifiedAt = now
	if p.Kind == 0 {
		p.Kind = domain.AddressPoolKindExternalPublic
	}
	f.kr.SeedAddressPool(&kachorepo.AddressPoolRecord{AddressPool: *p})
}

func (f *updateHandlerFixture) get(t *testing.T, id string) *domain.AddressPool {
	t.Helper()
	rd, err := f.kr.Reader(context.Background())
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	rec, err := rd.AddressPools().Get(context.Background(), id)
	require.NoError(t, err)
	out := rec.AddressPool
	return &out
}

// TestHandler_Update_PerFieldFlagToMaskPath — table-driven: каждая строка задает
// request-флаги и проверяет, какие поля изменились (= поле попало в набор), а
// какие — нет (= флаг не выставлен).
func TestHandler_Update_PerFieldFlagToMaskPath(t *testing.T) {
	const (
		baseName     = "base-name"
		baseDesc     = "base-desc"
		baseDefault  = false
		basePriority = int32(3)
	)
	baseLabels := map[string]string{"k": "v"}
	baseSelLabels := map[string]string{"sel": "a"}

	cases := []struct {
		name string
		req  *vpcv1.UpdateAddressPoolRequest

		wantName     string
		wantDesc     string
		wantLabels   map[string]string
		wantDefault  bool
		wantSelLbls  map[string]string
		wantPriority int32
	}{
		{
			name: "name set (non-empty) → name обновлен, остальное нетронуто",
			req: &vpcv1.UpdateAddressPoolRequest{
				Name: "renamed",
			},
			wantName: "renamed", wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "description set (non-empty) → description обновлен",
			req: &vpcv1.UpdateAddressPoolRequest{
				Description: "new-desc",
			},
			wantName: baseName, wantDesc: "new-desc", wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name:     "name+description пустые → ничего не меняется (no-op)",
			req:      &vpcv1.UpdateAddressPoolRequest{},
			wantName: baseName, wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "replace_labels=true → labels заменены",
			req: &vpcv1.UpdateAddressPoolRequest{
				ReplaceLabels: true,
				Labels:        map[string]string{"new": "label"},
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: map[string]string{"new": "label"},
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "replace_labels=true + пустое тело → labels очищены",
			req: &vpcv1.UpdateAddressPoolRequest{
				ReplaceLabels: true,
				Labels:        nil,
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: nil,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "replace_labels=false → labels нетронуты, даже если тело задано",
			req: &vpcv1.UpdateAddressPoolRequest{
				ReplaceLabels: false,
				Labels:        map[string]string{"ignored": "yes"},
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "update_is_default=true → is_default переключен в true",
			req: &vpcv1.UpdateAddressPoolRequest{
				UpdateIsDefault: true,
				IsDefault:       true,
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: true, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "update_is_default=false → is_default нетронут, даже если тело true",
			req: &vpcv1.UpdateAddressPoolRequest{
				UpdateIsDefault: false,
				IsDefault:       true,
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "replace_selector_labels=true → selector_labels заменены",
			req: &vpcv1.UpdateAddressPoolRequest{
				ReplaceSelectorLabels: true,
				SelectorLabels:        map[string]string{"sel": "b"},
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: map[string]string{"sel": "b"}, wantPriority: basePriority,
		},
		{
			name: "update_selector_priority=true → selector_priority применен",
			req: &vpcv1.UpdateAddressPoolRequest{
				UpdateSelectorPriority: true,
				SelectorPriority:       9,
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: 9,
		},
		{
			name: "update_selector_priority=false → selector_priority нетронут",
			req: &vpcv1.UpdateAddressPoolRequest{
				UpdateSelectorPriority: false,
				SelectorPriority:       9,
			},
			wantName: baseName, wantDesc: baseDesc, wantLabels: baseLabels,
			wantDefault: baseDefault, wantSelLbls: baseSelLabels, wantPriority: basePriority,
		},
		{
			name: "все флаги выставлены → все мутабельные поля изменены",
			req: &vpcv1.UpdateAddressPoolRequest{
				Name:                   "all-renamed",
				Description:            "all-desc",
				ReplaceLabels:          true,
				Labels:                 map[string]string{"a": "1"},
				UpdateIsDefault:        true,
				IsDefault:              true,
				ReplaceSelectorLabels:  true,
				SelectorLabels:         map[string]string{"s": "x"},
				UpdateSelectorPriority: true,
				SelectorPriority:       11,
			},
			wantName: "all-renamed", wantDesc: "all-desc",
			wantLabels:  map[string]string{"a": "1"},
			wantDefault: true, wantSelLbls: map[string]string{"s": "x"}, wantPriority: 11,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpdateHandlerFixture(t)
			id := ids.NewID(ids.PrefixAddressPool)
			f.seed(t, &domain.AddressPool{
				ID:               id,
				Name:             baseName,
				Description:      baseDesc,
				Labels:           cloneMap(baseLabels),
				ZoneID:           "zone-c",
				V4CIDRBlocks:     []string{"198.51.100.0/24"},
				IsDefault:        baseDefault,
				SelectorLabels:   cloneMap(baseSelLabels),
				SelectorPriority: basePriority,
			})

			tc.req.PoolId = id
			out, err := f.h.Update(context.Background(), tc.req)
			require.NoError(t, err)
			require.NotNil(t, out)

			got := f.get(t, id)
			assert.Equal(t, tc.wantName, got.Name, "name")
			assert.Equal(t, tc.wantName, out.Name, "name (response)")
			assert.Equal(t, tc.wantDesc, got.Description, "description")
			assert.Equal(t, tc.wantDefault, got.IsDefault, "is_default")
			assert.Equal(t, tc.wantPriority, got.SelectorPriority, "selector_priority")
			assertMapEqual(t, tc.wantLabels, got.Labels, "labels")
			assertMapEqual(t, tc.wantSelLbls, got.SelectorLabels, "selector_labels")
		})
	}
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// assertMapEqual трактует nil и пустой map одинаково (replace пустым map =
// очистка; представление зависит от слоя).
func assertMapEqual(t *testing.T, want, got map[string]string, field string) {
	t.Helper()
	if len(want) == 0 {
		assert.Empty(t, got, field+" must be empty/cleared")
		return
	}
	assert.Equal(t, want, got, field)
}
