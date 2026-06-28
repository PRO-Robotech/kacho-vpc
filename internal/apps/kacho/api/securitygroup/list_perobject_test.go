// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

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

// Per-object filtered List + no-leak Get для SecurityGroupService: возвращаем
// ТОЛЬКО авторизованные subject'ом SG (relation viewer / FGA type
// vpc_security_group), read==enforce, fail-closed при недоступном iam,
// wildcard scope_grant → all-in-scope, empty grant → пусто (no-leak).

// fakeListFilter — in-memory ListFilter для unit-тестов: запоминает аргументы
// вызова (subject, resourceType, action) и отдает сконфигурированное решение.
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

// seedSecurityGroupsLabeled вставляет non-default SG с заданными id в
// project/network. Non-default — чтобы не упереться в инвариант
// one-default-SG-per-network.
func seedSecurityGroupsLabeled(t *testing.T, kr *kachomock.Repository, projectID, networkID string, sgIDs ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range sgIDs {
		sg := &domain.SecurityGroup{
			ID:        id,
			ProjectID: projectID,
			NetworkID: networkID,
			Name:      domain.RcNameVPC("sg-" + id),
		}
		if _, ierr := w.SecurityGroups().Insert(context.Background(), sg); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// List возвращает ровно per-object allowed-набор.
func TestSecurityGroupListPerObject_ReturnsOnlyAllowed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_aaa", "sgr_bbb", "sgr_ccc")

	filter := &fakeListFilter{allowed: []string{"sgr_aaa", "sgr_bbb"}}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	sgs, _, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, sgs, 2)
	got := map[string]bool{}
	for _, sg := range sgs {
		got[sg.ID] = true
	}
	assert.True(t, got["sgr_aaa"])
	assert.True(t, got["sgr_bbb"])
	assert.False(t, got["sgr_ccc"], "sgr_ccc not in the allowed set → must not appear")

	// read==enforce: фильтр зовется с read-verb, отображенным на viewer
	// (action vpc.securityGroups.list, FGA type vpc_security_group).
	assert.Equal(t, "user:usr_alice", filter.gotSubject)
	assert.Equal(t, "vpc_security_group", filter.gotResourceType)
	assert.Equal(t, "vpc.securityGroups.list", filter.gotAction)
}

// no-leak: объект вне всех grant'ов отсутствует в List.
func TestSecurityGroupListPerObject_NoLeak(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_visible", "sgr_secret")

	filter := &fakeListFilter{allowed: []string{"sgr_visible"}}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	sgs, _, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, sgs, 1)
	assert.Equal(t, "sgr_visible", sgs[0].ID)
}

// empty grant: subject без grant'ов → пустой список (НЕ unfiltered).
func TestSecurityGroupListPerObject_EmptyGrantEmptyList(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_a", "sgr_b")

	filter := &fakeListFilter{allowed: nil}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	sgs, next, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, sgs)
	assert.Empty(t, next)
}

// global / wildcard: scope_grant → all-in-scope. Фильтр возвращает bypass=true,
// use-case отдает все строки в project-scope.
func TestSecurityGroupListPerObject_WildcardBypassReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_a", "sgr_b", "sgr_c")

	filter := &fakeListFilter{bypass: true}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	sgs, _, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, sgs, 3)
}

// fail-closed: iam недоступен → Unavailable (НЕ unfiltered, НЕ молча пусто).
func TestSecurityGroupListPerObject_FailClosedUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_a")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// fail-closed (plain error): не-status ошибка тоже маппится в non-OK код, не
// проскакивает молча как unfiltered.
func TestSecurityGroupListPerObject_FailClosedPlainError(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_a")

	filter := &fakeListFilter{err: errors.New("boom")}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.NotEqual(t, codes.OK, st.Code())
}

// nil filter → unfiltered passthrough (list-filter отключен).
func TestSecurityGroupListPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_a", "sgr_b")

	uc := NewListSecurityGroupsUseCase(kr, nil)
	sgs, _, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, sgs, 2)
}

// empty subject (system principal) → unfiltered passthrough (без FGA-вызова).
func TestSecurityGroupListPerObject_SystemSubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_a", "sgr_b")

	filter := &fakeListFilter{allowed: []string{"sgr_a"}}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	sgs, _, err := uc.Execute(context.Background(), authzfilter.SystemSubject, SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, sgs, 2)
	assert.Equal(t, 0, filter.calls, "explicit system principal → passthrough, no FGA ListObjects call")
}

// project_id по-прежнему обязателен (контракт не меняется).
func TestSecurityGroupListPerObject_ProjectIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewListSecurityGroupsUseCase(kr, &fakeListFilter{bypass: true})

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SecurityGroupFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// no-leak Get: subject без grant'а на существующую SG → NotFound (НЕ
// PermissionDenied), тот же текст, что и для несуществующей SG.
func TestSecurityGroupGetPerObject_NoLeakNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_hidden")

	// subject'у выдана другая SG, НЕ sgr_hidden.
	filter := &fakeListFilter{allowed: []string{"sgr_other"}}
	uc := NewGetSecurityGroupUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "sgr_hidden")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code(), "ungranted existing SG → NotFound, not PermissionDenied")
	assert.Contains(t, st.Message(), "not found")
}

// read==enforce: subject'у выдана SG → Get ее возвращает.
func TestSecurityGroupGetPerObject_GrantedReturnsResource(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_visible")

	filter := &fakeListFilter{allowed: []string{"sgr_visible"}}
	uc := NewGetSecurityGroupUseCase(kr, filter)

	got, err := uc.Execute(context.Background(), "user:usr_alice", "sgr_visible")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "sgr_visible", got.ID)
}

// wildcard bypass → Get возвращает ресурс даже без явного per-id grant'а.
func TestSecurityGroupGetPerObject_WildcardBypass(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_any")

	uc := NewGetSecurityGroupUseCase(kr, &fakeListFilter{bypass: true})
	got, err := uc.Execute(context.Background(), "user:usr_alice", "sgr_any")
	require.NoError(t, err)
	assert.Equal(t, "sgr_any", got.ID)
}

// fail-closed: ошибка iam на Get-enforce → Unavailable, а не ресурс.
func TestSecurityGroupGetPerObject_FailClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_x")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewGetSecurityGroupUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "sgr_x")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// nil filter / empty subject → без enforce (authz делает interceptor).
func TestSecurityGroupGetPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_y")

	uc := NewGetSecurityGroupUseCase(kr, nil)
	got, err := uc.Execute(context.Background(), "user:usr_alice", "sgr_y")
	require.NoError(t, err)
	assert.Equal(t, "sgr_y", got.ID)
}

// No-leak (defense-in-depth): пустой subject (principal не извлечён — anon /
// gateway не проставил identity) при ВКЛЮЧЁННОМ фильтре → fail-closed (пустой
// список), НЕ unfiltered passthrough. «Не знаю, кто ты» != «доверенный system».
func TestSecurityGroupListPerObject_EmptySubjectFailsClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSecurityGroupsLabeled(t, kr, "prj_1", "enp_net1", "sgr_a", "sgr_b")

	filter := &fakeListFilter{allowed: []string{"sgs_unused"}}
	uc := NewListSecurityGroupsUseCase(kr, filter)

	sgs, _, err := uc.Execute(context.Background(), "", SecurityGroupFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, sgs, "empty subject + filter enabled -> fail-closed empty, NOT leak")
}
