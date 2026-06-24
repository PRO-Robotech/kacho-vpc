// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package routetable

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// Per-object фильтрованный List и no-leak Get для RouteTableService: возвращаем
// ТОЛЬКО авторизованные subject'у route-table'ы (relation viewer, FGA-тип
// vpc_route_table), read==enforce, fail-closed при недоступном iam, wildcard
// scope_grant → все в скоупе, пустой grant → пусто (no-leak).

// fakeListFilter — in-memory ListFilter для unit-тестов. Запоминает аргументы
// (subject, resourceType, action), с которыми его позвали, и возвращает заранее
// заданное решение.
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

// seedRouteTablesLabeled вставляет route-table'ы с заданными id в проект.
func seedRouteTablesLabeled(t *testing.T, kr *kachomock.Repository, projectID, networkID string, rtIDs ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range rtIDs {
		rt := &domain.RouteTable{
			ID:        id,
			ProjectID: projectID,
			NetworkID: networkID,
			Name:      domain.RcNameVPC("rt-" + id),
		}
		if _, ierr := w.RouteTables().Insert(context.Background(), rt); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// List возвращает ровно per-object разрешенный набор.
func TestRouteTableListPerObject_ReturnsOnlyAllowed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_aaa", "rtb_bbb", "rtb_ccc")

	filter := &fakeListFilter{allowed: []string{"rtb_aaa", "rtb_bbb"}}
	uc := NewListRouteTablesUseCase(kr, filter)

	rts, _, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, rts, 2)
	got := map[string]bool{}
	for _, rt := range rts {
		got[rt.ID] = true
	}
	assert.True(t, got["rtb_aaa"])
	assert.True(t, got["rtb_bbb"])
	assert.False(t, got["rtb_ccc"], "rtb_ccc not in the allowed set → must not appear")

	// read==enforce: фильтр зовется с read-verb, маппнутым в viewer
	// (action vpc.routeTables.list, FGA-тип vpc_route_table).
	assert.Equal(t, "user:usr_alice", filter.gotSubject)
	assert.Equal(t, "vpc_route_table", filter.gotResourceType)
	assert.Equal(t, "vpc.routeTables.list", filter.gotAction)
}

// no-leak: объект вне всех grant'ов отсутствует в List.
func TestRouteTableListPerObject_NoLeak(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_visible", "rtb_secret")

	filter := &fakeListFilter{allowed: []string{"rtb_visible"}}
	uc := NewListRouteTablesUseCase(kr, filter)

	rts, _, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, rts, 1)
	assert.Equal(t, "rtb_visible", rts[0].ID)
}

// Пустой grant: subject без grant'а → пустой список (НЕ нефильтрованный).
func TestRouteTableListPerObject_EmptyGrantEmptyList(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_a", "rtb_b")

	filter := &fakeListFilter{allowed: nil}
	uc := NewListRouteTablesUseCase(kr, filter)

	rts, next, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, rts)
	assert.Empty(t, next)
}

// global / wildcard: scope_grant → все в скоупе. Фильтр возвращает bypass=true,
// use-case отдает все строки в рамках проекта.
func TestRouteTableListPerObject_WildcardBypassReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_a", "rtb_b", "rtb_c")

	filter := &fakeListFilter{bypass: true}
	uc := NewListRouteTablesUseCase(kr, filter)

	rts, _, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, rts, 3)
}

// fail-closed: iam недоступен → Unavailable (НЕ нефильтрованный, НЕ молча пустой).
func TestRouteTableListPerObject_FailClosedUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_a")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewListRouteTablesUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// fail-closed (plain error): не-status ошибка тоже маппится в non-OK код, никогда
// не проходит молча как нефильтрованная.
func TestRouteTableListPerObject_FailClosedPlainError(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_a")

	filter := &fakeListFilter{err: errors.New("boom")}
	uc := NewListRouteTablesUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.NotEqual(t, codes.OK, st.Code())
}

// nil-фильтр → нефильтрованный passthrough (list-filter выключен).
func TestRouteTableListPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_a", "rtb_b")

	uc := NewListRouteTablesUseCase(kr, nil)
	rts, _, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, rts, 2)
}

// пустой subject (system principal) → нефильтрованный passthrough (без вызова FGA).
func TestRouteTableListPerObject_EmptySubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_a", "rtb_b")

	filter := &fakeListFilter{allowed: []string{"rtb_a"}}
	uc := NewListRouteTablesUseCase(kr, filter)

	rts, _, err := uc.Execute(context.Background(), "", RouteTableFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, rts, 2)
	assert.Equal(t, 0, filter.calls, "system principal must not trigger an FGA ListObjects call")
}

// project_id по-прежнему обязателен (контракт не меняется).
func TestRouteTableListPerObject_ProjectIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewListRouteTablesUseCase(kr, &fakeListFilter{bypass: true})

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", RouteTableFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// no-leak Get: subject без grant'а на существующий route-table → NotFound
// (НЕ PermissionDenied), тот же текст, что для несуществующего route-table.
func TestRouteTableGetPerObject_NoLeakNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_hidden")

	// subject'у выдан какой-то другой route-table, НЕ rtb_hidden.
	filter := &fakeListFilter{allowed: []string{"rtb_other"}}
	uc := NewGetRouteTableUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "rtb_hidden")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code(), "ungranted existing route table → NotFound, not PermissionDenied")
	assert.Contains(t, st.Message(), "not found")
}

// read==enforce: route-table выдан subject'у → Get его возвращает.
func TestRouteTableGetPerObject_GrantedReturnsResource(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_visible")

	filter := &fakeListFilter{allowed: []string{"rtb_visible"}}
	uc := NewGetRouteTableUseCase(kr, filter)

	got, err := uc.Execute(context.Background(), "user:usr_alice", "rtb_visible")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "rtb_visible", got.ID)
}

// wildcard bypass → Get возвращает ресурс даже без явного per-id grant'а.
func TestRouteTableGetPerObject_WildcardBypass(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_any")

	uc := NewGetRouteTableUseCase(kr, &fakeListFilter{bypass: true})
	got, err := uc.Execute(context.Background(), "user:usr_alice", "rtb_any")
	require.NoError(t, err)
	assert.Equal(t, "rtb_any", got.ID)
}

// fail-closed: ошибка iam при enforce Get → Unavailable, а не ресурс.
func TestRouteTableGetPerObject_FailClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_x")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewGetRouteTableUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "rtb_x")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// nil-фильтр / пустой subject → без enforce (authz делает interceptor).
func TestRouteTableGetPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedRouteTablesLabeled(t, kr, "prj_1", "enp_net1", "rtb_y")

	uc := NewGetRouteTableUseCase(kr, nil)
	got, err := uc.Execute(context.Background(), "user:usr_alice", "rtb_y")
	require.NoError(t, err)
	assert.Equal(t, "rtb_y", got.ID)
}
