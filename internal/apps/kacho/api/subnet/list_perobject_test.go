// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

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

// Per-object filtered List.
//
// SubnetService.List обязан вернуть ТОЛЬКО те подсети, которые caller-subject
// вправе видеть (per-object FGA ListObjects поверх материализованных tuples +
// scope_grant), а не project-level решение «все или ничего». read==enforce parity:
// видимый набор == Check-allow набор; fail-closed, если iam недоступен; pagination
// применяется ПОСЛЕ фильтра.
//
// Тесты гоняют per-object port `ListFilter`. Use-case зовет ListAllowedIDs с FGA
// resource_type (vpc_subnet) и action-verb (vpc.subnets.list → viewer), затем
// сужает SQL через repo.ListByIDs (WHERE id = ANY).

// fakeListFilter — in-memory ListFilter для unit-тестов. Запоминает, с какими
// (subject, resourceType, action) его вызвали, и возвращает настраиваемое решение.
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

func makeSubnetPerObjectUC(kr *kachomock.Repository, filter ListFilter) *ListSubnetsUseCase {
	return NewListSubnetsUseCase(kr, filter)
}

// seedSubnetsLabeled вставляет подсети с заданными id в project/network.
func seedSubnetsLabeled(t *testing.T, kr *kachomock.Repository, projectID, networkID string, subnetIDs ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range subnetIDs {
		s := &domain.Subnet{ID: id, ProjectID: projectID, NetworkID: networkID, Name: domain.RcNameVPC("sub-" + id)}
		if _, ierr := w.Subnets().Insert(context.Background(), s); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// List возвращает ровно per-object allowed-набор (объединение веток разрешается
// внутри FGA ListObjects; use-case лишь пересекает его со строками проекта).
func TestSubnetListPerObject_ReturnsOnlyAllowed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_aaa", "e9b_bbb", "e9b_ccc")

	filter := &fakeListFilter{allowed: []string{"e9b_aaa", "e9b_bbb"}}
	uc := makeSubnetPerObjectUC(kr, filter)

	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, subs, 2)
	got := map[string]bool{}
	for _, s := range subs {
		got[s.ID] = true
	}
	assert.True(t, got["e9b_aaa"])
	assert.True(t, got["e9b_bbb"])
	assert.False(t, got["e9b_ccc"], "e9b_ccc not in the allowed set → must not appear")

	// read==enforce: фильтр вызывается с read-verb, смапленным на viewer
	// (action vpc.subnets.list, FGA-тип vpc_subnet).
	assert.Equal(t, "user:usr_alice", filter.gotSubject)
	assert.Equal(t, "vpc_subnet", filter.gotResourceType)
	assert.Equal(t, "vpc.subnets.list", filter.gotAction)
}

// no-leak: объект вне всех грантов отсутствует в List.
func TestSubnetListPerObject_NoLeak(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_visible", "e9b_secret")

	filter := &fakeListFilter{allowed: []string{"e9b_visible"}}
	uc := makeSubnetPerObjectUC(kr, filter)

	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, "e9b_visible", subs[0].ID)
}

// empty grant: subject без гранта → пустой list (НЕ unfiltered).
func TestSubnetListPerObject_EmptyGrantEmptyList(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_a", "e9b_b")

	filter := &fakeListFilter{allowed: nil}
	uc := makeSubnetPerObjectUC(kr, filter)

	subs, next, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, subs)
	assert.Empty(t, next)
}

// wildcard scope_grant → all-in-scope: фильтр возвращает bypass=true, use-case
// отдает все project-scoped строки.
func TestSubnetListPerObject_WildcardBypassReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_a", "e9b_b", "e9b_c")

	filter := &fakeListFilter{bypass: true}
	uc := makeSubnetPerObjectUC(kr, filter)

	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, subs, 3)
}

// fail-closed: iam недоступен → Unavailable (НЕ unfiltered, НЕ молча пусто).
func TestSubnetListPerObject_FailClosedUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_a")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := makeSubnetPerObjectUC(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// fail-closed (plain error): не-status ошибка тоже маппится в non-OK код, никогда
// не проходит молча как unfiltered.
func TestSubnetListPerObject_FailClosedPlainError(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_a")

	filter := &fakeListFilter{err: errors.New("boom")}
	uc := makeSubnetPerObjectUC(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.NotEqual(t, codes.OK, st.Code())
}

// nil filter → unfiltered passthrough (dev / list-filter отключен).
func TestSubnetListPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_a", "e9b_b")

	uc := NewListSubnetsUseCase(kr, nil)
	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, subs, 2)
}

// empty subject (system principal) → unfiltered passthrough (без вызова FGA).
func TestSubnetListPerObject_SystemSubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_a", "e9b_b")

	filter := &fakeListFilter{allowed: []string{"e9b_a"}}
	uc := makeSubnetPerObjectUC(kr, filter)

	subs, _, err := uc.Execute(context.Background(), authzfilter.SystemSubject, SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, subs, 2)
	assert.Equal(t, 0, filter.calls, "explicit system principal → passthrough, no FGA ListObjects call")
}

// no-leak Get: subject без гранта на существующую подсеть → NotFound (НЕ
// PermissionDenied), тот же текст, что и для несуществующей подсети.
func TestSubnetGetPerObject_NoLeakNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_hidden")

	// subject'у выдан грант на другую подсеть, НЕ на e9b_hidden.
	filter := &fakeListFilter{allowed: []string{"e9b_other"}}
	uc := NewGetSubnetUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "e9b_hidden")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code(), "ungranted existing subnet → NotFound, not PermissionDenied")
	assert.Contains(t, st.Message(), "not found")
}

// read==enforce: subject'у выдан грант на подсеть → Get ее возвращает.
func TestSubnetGetPerObject_GrantedReturnsResource(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_visible")

	filter := &fakeListFilter{allowed: []string{"e9b_visible"}}
	uc := NewGetSubnetUseCase(kr, filter)

	got, err := uc.Execute(context.Background(), "user:usr_alice", "e9b_visible")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "e9b_visible", got.ID)
}

// wildcard bypass → Get возвращает ресурс даже без явного per-id гранта.
func TestSubnetGetPerObject_WildcardBypass(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_any")

	uc := NewGetSubnetUseCase(kr, &fakeListFilter{bypass: true})
	got, err := uc.Execute(context.Background(), "user:usr_alice", "e9b_any")
	require.NoError(t, err)
	assert.Equal(t, "e9b_any", got.ID)
}

// fail-closed: ошибка iam при Get-enforce → Unavailable, а не сам ресурс.
func TestSubnetGetPerObject_FailClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_x")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewGetSubnetUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "e9b_x")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// nil filter / empty subject → enforce пропускается (authz делает interceptor).
func TestSubnetGetPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_y")

	uc := NewGetSubnetUseCase(kr, nil)
	got, err := uc.Execute(context.Background(), "user:usr_alice", "e9b_y")
	require.NoError(t, err)
	assert.Equal(t, "e9b_y", got.ID)
}

// project_id по-прежнему обязателен (контракт не меняется).
func TestSubnetListPerObject_ProjectIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := makeSubnetPerObjectUC(kr, &fakeListFilter{bypass: true})

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// No-leak (defense-in-depth): пустой subject (principal не извлечен — anon /
// gateway не проставил identity) при ВКЛЮЧЕННОМ фильтре → fail-closed (пустой
// список), НЕ unfiltered passthrough. «Не знаю, кто ты» != «доверенный system».
func TestSubnetListPerObject_EmptySubjectFailsClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnetsLabeled(t, kr, "prj_1", "enp_net1", "e9b_a", "e9b_b")

	filter := &fakeListFilter{allowed: []string{"subs_unused"}}
	uc := makeSubnetPerObjectUC(kr, filter)

	subs, _, err := uc.Execute(context.Background(), "", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, subs, "empty subject + filter enabled -> fail-closed empty, NOT leak")
}
