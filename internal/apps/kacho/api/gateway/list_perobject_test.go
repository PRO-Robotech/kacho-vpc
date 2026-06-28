// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// Тесты per-object фильтрации List + no-leak Get для GatewayService: List
// возвращает ТОЛЬКО авторизованные subject'у gateway'и (relation viewer / FGA
// type vpc_gateway), read==enforce, fail-closed при недоступности iam, wildcard
// scope_grant → all-in-scope, пустой grant → пустой результат (no-leak).

// fakeListFilter — in-memory ListFilter для unit-тестов: запоминает
// (subject, resourceType, action), с которыми его вызвали, и возвращает
// сконфигурированное решение.
type fakeListFilter struct {
	allowed []string
	bypass  bool
	err     error

	gotSubject      string
	gotResourceType string
	gotAction       string
	calls           int
}

func (f *fakeListFilter) ListAllowedIDs(_ context.Context, subject, resourceType, action string) ([]string, bool, error) {
	f.calls++
	f.gotSubject = subject
	f.gotResourceType = resourceType
	f.gotAction = action
	if f.err != nil {
		return nil, false, f.err
	}
	return f.allowed, f.bypass, nil
}

// seedGatewaysLabeled вставляет в проект gateway'и с указанными id.
func seedGatewaysLabeled(t *testing.T, kr *kachomock.Repository, projectID string, gwIDs ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range gwIDs {
		g := &domain.Gateway{
			ID:          id,
			ProjectID:   projectID,
			Name:        domain.RcNameVPC("gw-" + id),
			GatewayType: domain.GatewayTypeSharedEgress,
		}
		if _, ierr := w.Gateways().Insert(context.Background(), g); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// List возвращает ровно per-object разрешенный набор.
func TestGatewayListPerObject_ReturnsOnlyAllowed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_aaa", "gtw_bbb", "gtw_ccc")

	filter := &fakeListFilter{allowed: []string{"gtw_aaa", "gtw_bbb"}}
	uc := NewListGatewaysUseCase(kr, filter)

	gws, _, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, gws, 2)
	got := map[string]bool{}
	for _, g := range gws {
		got[g.ID] = true
	}
	assert.True(t, got["gtw_aaa"])
	assert.True(t, got["gtw_bbb"])
	assert.False(t, got["gtw_ccc"], "gtw_ccc not in the allowed set → must not appear")

	// read==enforce: filter вызывается с read-verb, смапленным на viewer
	// (action vpc.gateways.list, FGA type vpc_gateway).
	assert.Equal(t, "user:usr_alice", filter.gotSubject)
	assert.Equal(t, "vpc_gateway", filter.gotResourceType)
	assert.Equal(t, "vpc.gateways.list", filter.gotAction)
}

// no-leak: объект вне всех grant'ов отсутствует в List.
func TestGatewayListPerObject_NoLeak(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_visible", "gtw_secret")

	filter := &fakeListFilter{allowed: []string{"gtw_visible"}}
	uc := NewListGatewaysUseCase(kr, filter)

	gws, _, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, gws, 1)
	assert.Equal(t, "gtw_visible", gws[0].ID)
}

// empty grant: subject без grant'а → пустой список (НЕ нефильтрованный).
func TestGatewayListPerObject_EmptyGrantEmptyList(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_a", "gtw_b")

	filter := &fakeListFilter{allowed: nil}
	uc := NewListGatewaysUseCase(kr, filter)

	gws, next, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, gws)
	assert.Empty(t, next)
}

// global / wildcard: scope_grant на весь scope → all-in-scope. Filter возвращает
// bypass=true; use-case отдает все строки в пределах project-scope.
func TestGatewayListPerObject_WildcardBypassReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_a", "gtw_b", "gtw_c")

	filter := &fakeListFilter{bypass: true}
	uc := NewListGatewaysUseCase(kr, filter)

	gws, _, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, gws, 3)
}

// fail-closed: iam недоступен → Unavailable (НЕ нефильтрованный список и НЕ молча
// пустой).
func TestGatewayListPerObject_FailClosedUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_a")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewListGatewaysUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// fail-closed (plain error): не-status ошибка тоже маппится в non-OK код, никогда
// не проходит молча нефильтрованной.
func TestGatewayListPerObject_FailClosedPlainError(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_a")

	filter := &fakeListFilter{err: errors.New("boom")}
	uc := NewListGatewaysUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.NotEqual(t, codes.OK, st.Code())
}

// nil filter → нефильтрованный passthrough (list-filter выключен).
func TestGatewayListPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_a", "gtw_b")

	uc := NewListGatewaysUseCase(kr, nil)
	gws, _, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, gws, 2)
}

// empty subject (system principal) → нефильтрованный passthrough (без FGA-вызова).
func TestGatewayListPerObject_SystemSubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_a", "gtw_b")

	filter := &fakeListFilter{allowed: []string{"gtw_a"}}
	uc := NewListGatewaysUseCase(kr, filter)

	gws, _, err := uc.Execute(context.Background(), authzfilter.SystemSubject, GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, gws, 2)
	assert.Equal(t, 0, filter.calls, "explicit system principal → passthrough, no FGA ListObjects call")
}

// project_id по-прежнему обязателен (контракт неизменен).
func TestGatewayListPerObject_ProjectIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewListGatewaysUseCase(kr, &fakeListFilter{bypass: true})

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", GatewayFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// no-leak Get: subject без grant'а на существующий gateway → NotFound (НЕ
// PermissionDenied), с тем же текстом, что и для несуществующего gateway.
func TestGatewayGetPerObject_NoLeakNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_hidden")

	// subject'у выдан grant на другой gateway, НЕ на gtw_hidden.
	filter := &fakeListFilter{allowed: []string{"gtw_other"}}
	uc := NewGetGatewayUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "gtw_hidden")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code(), "ungranted existing gateway → NotFound, not PermissionDenied")
	assert.Contains(t, st.Message(), "not found")
}

// read==enforce: subject'у выдан gateway → Get его возвращает.
func TestGatewayGetPerObject_GrantedReturnsResource(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_visible")

	filter := &fakeListFilter{allowed: []string{"gtw_visible"}}
	uc := NewGetGatewayUseCase(kr, filter)

	got, err := uc.Execute(context.Background(), "user:usr_alice", "gtw_visible")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "gtw_visible", got.ID)
}

// wildcard bypass → Get возвращает ресурс даже без явного per-id grant'а.
func TestGatewayGetPerObject_WildcardBypass(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_any")

	uc := NewGetGatewayUseCase(kr, &fakeListFilter{bypass: true})
	got, err := uc.Execute(context.Background(), "user:usr_alice", "gtw_any")
	require.NoError(t, err)
	assert.Equal(t, "gtw_any", got.ID)
}

// fail-closed: ошибка iam при enforce в Get → Unavailable, а не ресурс.
func TestGatewayGetPerObject_FailClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_x")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewGetGatewayUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "gtw_x")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// nil filter / empty subject → без enforce (authz делает interceptor).
func TestGatewayGetPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_y")

	uc := NewGetGatewayUseCase(kr, nil)
	got, err := uc.Execute(context.Background(), "user:usr_alice", "gtw_y")
	require.NoError(t, err)
	assert.Equal(t, "gtw_y", got.ID)
}

// No-leak (defense-in-depth): пустой subject (principal не извлечён — anon /
// gateway не проставил identity) при ВКЛЮЧЁННОМ фильтре → fail-closed (пустой
// список), НЕ unfiltered passthrough. «Не знаю, кто ты» != «доверенный system».
func TestGatewayListPerObject_EmptySubjectFailsClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedGatewaysLabeled(t, kr, "prj_1", "gtw_a", "gtw_b")

	filter := &fakeListFilter{allowed: []string{"gws_unused"}}
	uc := NewListGatewaysUseCase(kr, filter)

	gws, _, err := uc.Execute(context.Background(), "", GatewayFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, gws, "empty subject + filter enabled -> fail-closed empty, NOT leak")
}
