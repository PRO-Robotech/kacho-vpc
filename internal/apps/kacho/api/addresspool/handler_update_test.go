// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package addresspool — handler_update_test.go: unit-тесты тонкого transport-слоя
// Handler.Update под единой update_mask-дисциплиной (Группа D среза AddressPool
// parity).
//
// UpdateAddressPoolRequest несет google.protobuf.FieldMask update_mask (прежние
// per-field-флаги replace_labels/update_is_default/... удалены). Тест фиксирует
// wire-контракт: какие поля заданы в update_mask → те и мутируются; immutable/
// unknown в mask → InvalidArgument; пустой/отсутствующий mask → full-object PATCH;
// частичного применения нет (валидация до записи). Прогоняется реальный
// UpdateAddressPoolUseCase поверх kachomock.Repository.
package addresspool

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// updateHandlerFixture — Handler с реальным UpdateAddressPoolUseCase поверх
// kachomock; остальные use-case'ы не нужны (тест зовет Update + Get).
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
		NewGetAddressPoolUseCase(kr),    // get — для проверки persisted-состояния
		nil, nil, nil, nil, nil, nil, nil,
	)
	return &updateHandlerFixture{h: h, kr: kr}
}

// seed кладет pool в начальном состоянии (минуя use-case).
func (f *updateHandlerFixture) seed(t *testing.T, p *domain.AddressPool) {
	t.Helper()
	now := time.Now().UTC()
	if p.ID == "" {
		p.ID = ids.NewID(ids.PrefixAddressPool)
	}
	if p.Kind == 0 {
		p.Kind = domain.AddressPoolKindExternalPublic
	}
	f.kr.SeedAddressPool(&kachorepo.AddressPoolRecord{AddressPool: *p, CreatedAt: now, ModifiedAt: now})
}

// getPB достает текущее состояние пула как proto через handler.Get.
func (f *updateHandlerFixture) getPB(t *testing.T, id string) *vpcv1.AddressPool {
	t.Helper()
	out, err := f.h.Get(context.Background(), &vpcv1.GetAddressPoolRequest{PoolId: id})
	require.NoError(t, err)
	return out
}

func mask(paths ...string) *fieldmaskpb.FieldMask {
	return &fieldmaskpb.FieldMask{Paths: paths}
}

// fieldViolationDescs — описания BadRequest-field-violation'ов gRPC-статуса
// (frozen-тексты валидации живут в details, а не в top-level message —
// corevalidate.UpdateMask/serviceerr.FromValidation кладут их сюда).
func fieldViolationDescs(t *testing.T, err error) []string {
	t.Helper()
	st, ok := status.FromError(err)
	require.True(t, ok, "expected grpc status error")
	var out []string
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, fv := range br.GetFieldViolations() {
				out = append(out, fv.GetField()+": "+fv.GetDescription())
			}
		}
	}
	return out
}

// vpc8G-D1 — mask=set одного поля → применяется только оно (persisted).
func TestHandler_vpc8G_D1_Update_MaskName_OnlyNameChanges(t *testing.T) {
	f := newUpdateHandlerFixture(t)
	id := ids.NewID(ids.PrefixAddressPool)
	f.seed(t, &domain.AddressPool{
		ID: id, Name: "base", Description: "d", Labels: domain.RcLabels{"k": "v"},
		ZoneID: "region-1-a", V4CIDRBlocks: []string{"198.51.100.0/24"},
		IsDefault: false, SelectorPriority: 3,
	})

	out, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
		PoolId: id, UpdateMask: mask("name"), Name: "renamed",
		// прочие поля тела игнорируются (не в mask)
		Description: "ignored", IsDefault: true, SelectorPriority: 99,
	})
	require.NoError(t, err)
	assert.Equal(t, "renamed", out.GetName())
	assert.Equal(t, "d", out.GetDescription(), "description not in mask → untouched")
	assert.False(t, out.GetIsDefault(), "is_default not in mask → untouched")
	assert.Equal(t, int32(3), out.GetSelectorPriority(), "selector_priority not in mask → untouched")

	got := f.getPB(t, id)
	assert.Equal(t, "renamed", got.GetName(), "persisted")
	assert.Equal(t, "d", got.GetDescription())
	assert.Equal(t, map[string]string{"k": "v"}, got.GetLabels())
}

// vpc8G-D2 — clear через mask + пустое значение (без legacy replace-флага).
func TestHandler_vpc8G_D2_Update_MaskClear(t *testing.T) {
	t.Run("clear labels via mask + empty map", func(t *testing.T) {
		f := newUpdateHandlerFixture(t)
		id := ids.NewID(ids.PrefixAddressPool)
		f.seed(t, &domain.AddressPool{
			ID: id, Description: "d", Labels: domain.RcLabels{"k": "v"},
			V4CIDRBlocks: []string{"198.51.100.0/24"},
		})
		out, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
			PoolId: id, UpdateMask: mask("labels"), Labels: map[string]string{},
		})
		require.NoError(t, err)
		assert.Empty(t, out.GetLabels(), "labels cleared via mask + empty body")
		assert.Empty(t, f.getPB(t, id).GetLabels(), "persisted cleared")
	})
	t.Run("clear description via mask + empty string", func(t *testing.T) {
		f := newUpdateHandlerFixture(t)
		id := ids.NewID(ids.PrefixAddressPool)
		f.seed(t, &domain.AddressPool{
			ID: id, Description: "d", V4CIDRBlocks: []string{"198.51.100.0/24"},
		})
		out, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
			PoolId: id, UpdateMask: mask("description"), Description: "",
		})
		require.NoError(t, err)
		assert.Equal(t, "", out.GetDescription())
		assert.Equal(t, "", f.getPB(t, id).GetDescription())
	})
}

// vpc8G-D3 — пустой/omitted mask → full-object PATCH.
func TestHandler_vpc8G_D3_Update_EmptyMask_FullPatch(t *testing.T) {
	f := newUpdateHandlerFixture(t)
	id := ids.NewID(ids.PrefixAddressPool)
	f.seed(t, &domain.AddressPool{
		ID: id, Name: "n", Description: "d", Labels: domain.RcLabels{"k": "v"},
		ZoneID: "region-1-a", V4CIDRBlocks: []string{"198.51.100.0/24"},
		IsDefault: true, SelectorLabels: domain.RcLabels{"s": "a"}, SelectorPriority: 5,
	})

	// omitted update_mask + тело {name:n2, description:d2}; прочие поля zero.
	out, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
		PoolId: id, Name: "n2", Description: "d2",
	})
	require.NoError(t, err)
	assert.Equal(t, "n2", out.GetName())
	assert.Equal(t, "d2", out.GetDescription())
	assert.Empty(t, out.GetLabels(), "full-PATCH: labels cleared by zero body")
	assert.False(t, out.GetIsDefault(), "full-PATCH: is_default → false")
	assert.Empty(t, out.GetSelectorLabels(), "full-PATCH: selector_labels cleared")
	assert.Equal(t, int32(0), out.GetSelectorPriority(), "full-PATCH: selector_priority → 0")
	// immutable zone_id не тронут.
	assert.Equal(t, "region-1-a", out.GetZoneId())
}

// vpc8G-D4 — unknown-поле в mask → InvalidArgument (camelCase→snake_case).
func TestHandler_vpc8G_D4_Update_UnknownMask_InvalidArgument(t *testing.T) {
	f := newUpdateHandlerFixture(t)
	id := ids.NewID(ids.PrefixAddressPool)
	f.seed(t, &domain.AddressPool{ID: id, Name: "n", V4CIDRBlocks: []string{"198.51.100.0/24"}})

	_, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
		PoolId: id, UpdateMask: mask("bogusField"), Name: "x",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, fieldViolationDescs(t, err),
		"update_mask: unknown field in update_mask: bogus_field")
	assert.Equal(t, "n", f.getPB(t, id).GetName(), "no mutation on bad mask")
}

// vpc8G-D5 — immutable-поле в mask → InvalidArgument.
func TestHandler_vpc8G_D5_Update_ImmutableMask_InvalidArgument(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"zoneId", "zone_id is immutable after AddressPool.Create"},
		{"kind", "kind is immutable after AddressPool.Create"},
		{"id", "id is immutable after AddressPool.Create"},
		{"v4CidrBlocks", "v4_cidr_blocks is immutable via Update; use AddCidrBlocks/RemoveCidrBlocks"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			f := newUpdateHandlerFixture(t)
			id := ids.NewID(ids.PrefixAddressPool)
			f.seed(t, &domain.AddressPool{ID: id, Name: "n", V4CIDRBlocks: []string{"198.51.100.0/24"}})

			_, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
				PoolId: id, UpdateMask: mask(tc.path),
			})
			require.Error(t, err)
			st, _ := status.FromError(err)
			assert.Equal(t, codes.InvalidArgument, st.Code())
			assert.Contains(t, st.Message(), tc.want)
		})
	}
}

// vpc8G-D6 — идемпотентный повтор Update (одинаковый mask + тело).
func TestHandler_vpc8G_D6_Update_Idempotent(t *testing.T) {
	f := newUpdateHandlerFixture(t)
	id := ids.NewID(ids.PrefixAddressPool)
	f.seed(t, &domain.AddressPool{ID: id, Name: "base", V4CIDRBlocks: []string{"198.51.100.0/24"}})

	req := &vpcv1.UpdateAddressPoolRequest{PoolId: id, UpdateMask: mask("name"), Name: "renamed"}
	out1, err := f.h.Update(context.Background(), req)
	require.NoError(t, err)
	out2, err := f.h.Update(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "renamed", out1.GetName())
	assert.Equal(t, "renamed", out2.GetName())
	assert.Equal(t, out1.GetName(), out2.GetName(), "repeat must not drift content")
}

// vpc8G-D7 — нет 2xx без примененного изменения (валидное поле + невалидное в одном mask).
func TestHandler_vpc8G_D7_Update_NoFalseSuccess(t *testing.T) {
	f := newUpdateHandlerFixture(t)
	id := ids.NewID(ids.PrefixAddressPool)
	f.seed(t, &domain.AddressPool{ID: id, Name: "ok", Description: "ok-desc", V4CIDRBlocks: []string{"198.51.100.0/24"}})

	long := make([]byte, 257)
	for i := range long {
		long[i] = 'x'
	}
	_, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
		PoolId: id, UpdateMask: mask("name", "description"),
		Name: "newname", Description: string(long),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code(), "bad description → InvalidArgument, NOT 2xx")

	got := f.getPB(t, id)
	assert.Equal(t, "ok", got.GetName(), "no partial apply: name unchanged")
	assert.Equal(t, "ok-desc", got.GetDescription(), "no partial apply: description unchanged")
}

// vpc8G-D8 — применение is_default / selector_priority через mask (camelCase path).
func TestHandler_vpc8G_D8_Update_DefaultAndPriority(t *testing.T) {
	f := newUpdateHandlerFixture(t)
	id := ids.NewID(ids.PrefixAddressPool)
	f.seed(t, &domain.AddressPool{
		ID: id, Name: "n", ZoneID: "region-1-a", V4CIDRBlocks: []string{"198.51.100.0/24"},
		IsDefault: false, SelectorPriority: 0,
	})

	out, err := f.h.Update(context.Background(), &vpcv1.UpdateAddressPoolRequest{
		PoolId: id, UpdateMask: mask("isDefault", "selectorPriority"),
		IsDefault: true, SelectorPriority: 50,
	})
	require.NoError(t, err)
	assert.True(t, out.GetIsDefault())
	assert.Equal(t, int32(50), out.GetSelectorPriority())

	got := f.getPB(t, id)
	assert.True(t, got.GetIsDefault(), "persisted")
	assert.Equal(t, int32(50), got.GetSelectorPriority())
	assert.Equal(t, "n", got.GetName(), "name untouched")
}
