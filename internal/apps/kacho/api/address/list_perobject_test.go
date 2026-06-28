// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

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

// Per-object filtered List + no-leak Get для AddressService: возвращаем ТОЛЬКО
// авторизованные субъекту адреса (relation viewer / FGA type vpc_address),
// read==enforce, fail-closed при недоступном iam, wildcard scope_grant →
// all-in-scope, пустой grant → пусто (no-leak).

// fakeListFilter — in-memory ListFilter для unit-тестов. Запоминает аргументы
// вызова (subject, resourceType, action) и возвращает настраиваемое решение.
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

// seedAddressesLabeled вставляет external-адреса с заданными id в проект.
func seedAddressesLabeled(t *testing.T, kr *kachomock.Repository, projectID string, addrIDs ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range addrIDs {
		a := &domain.Address{
			ID:        id,
			ProjectID: projectID,
			Name:      domain.RcNameVPC("addr-" + id),
			Type:      domain.AddressTypeExternal,
			IpVersion: domain.IpVersionIPv4,
		}
		if _, ierr := w.Addresses().Insert(context.Background(), a); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// List возвращает ровно per-object allowed-набор.
func TestAddressListPerObject_ReturnsOnlyAllowed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_aaa", "adr_bbb", "adr_ccc")

	filter := &fakeListFilter{allowed: []string{"adr_aaa", "adr_bbb"}}
	uc := NewListAddressesUseCase(kr, filter)

	addrs, _, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, addrs, 2)
	got := map[string]bool{}
	for _, a := range addrs {
		got[a.ID] = true
	}
	assert.True(t, got["adr_aaa"])
	assert.True(t, got["adr_bbb"])
	assert.False(t, got["adr_ccc"], "adr_ccc not in the allowed set → must not appear")

	// read==enforce: фильтр вызван с read-verb, смапленным в viewer
	// (action vpc.addresses.list, FGA type vpc_address).
	assert.Equal(t, "user:usr_alice", filter.gotSubject)
	assert.Equal(t, "vpc_address", filter.gotResourceType)
	assert.Equal(t, "vpc.addresses.list", filter.gotAction)
}

// no-leak: объект вне всех grant'ов отсутствует в List.
func TestAddressListPerObject_NoLeak(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_visible", "adr_secret")

	filter := &fakeListFilter{allowed: []string{"adr_visible"}}
	uc := NewListAddressesUseCase(kr, filter)

	addrs, _, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	assert.Equal(t, "adr_visible", addrs[0].ID)
}

// empty grant: субъект без grant'ов → пустой список (НЕ unfiltered).
func TestAddressListPerObject_EmptyGrantEmptyList(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_a", "adr_b")

	filter := &fakeListFilter{allowed: nil}
	uc := NewListAddressesUseCase(kr, filter)

	addrs, next, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, addrs)
	assert.Empty(t, next)
}

// global / wildcard: wildcard scope_grant → all-in-scope. Фильтр возвращает
// bypass=true; use-case отдает все project-scoped строки.
func TestAddressListPerObject_WildcardBypassReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_a", "adr_b", "adr_c")

	filter := &fakeListFilter{bypass: true}
	uc := NewListAddressesUseCase(kr, filter)

	addrs, _, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, addrs, 3)
}

// fail-closed: iam unavailable → Unavailable (НЕ unfiltered, НЕ молча пусто).
func TestAddressListPerObject_FailClosedUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_a")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewListAddressesUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// fail-closed (plain error): non-status ошибка тоже маппится в non-OK код,
// никогда не проходит молча unfiltered.
func TestAddressListPerObject_FailClosedPlainError(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_a")

	filter := &fakeListFilter{err: errors.New("boom")}
	uc := NewListAddressesUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.NotEqual(t, codes.OK, st.Code())
}

// nil filter → unfiltered passthrough (list-filter отключен).
func TestAddressListPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_a", "adr_b")

	uc := NewListAddressesUseCase(kr, nil)
	addrs, _, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, addrs, 2)
}

// empty subject (system principal) → unfiltered passthrough (без FGA-вызова).
func TestAddressListPerObject_SystemSubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_a", "adr_b")

	filter := &fakeListFilter{allowed: []string{"adr_a"}}
	uc := NewListAddressesUseCase(kr, filter)

	addrs, _, err := uc.Execute(context.Background(), authzfilter.SystemSubject, AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, addrs, 2)
	assert.Equal(t, 0, filter.calls, "explicit system principal → passthrough, no FGA ListObjects call")
}

// project_id остается обязательным.
func TestAddressListPerObject_ProjectIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewListAddressesUseCase(kr, &fakeListFilter{bypass: true})

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", AddressFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// no-leak Get: субъект без grant'а на существующий адрес → NotFound
// (НЕ PermissionDenied), тот же текст, что и для несуществующего адреса.
func TestAddressGetPerObject_NoLeakNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_hidden")

	// субъекту выдан какой-то другой адрес, НЕ adr_hidden.
	filter := &fakeListFilter{allowed: []string{"adr_other"}}
	uc := NewGetAddressUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "adr_hidden")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code(), "ungranted existing address → NotFound, not PermissionDenied")
	assert.Contains(t, st.Message(), "not found")
}

// read==enforce: субъекту выдан адрес → Get его возвращает.
func TestAddressGetPerObject_GrantedReturnsResource(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_visible")

	filter := &fakeListFilter{allowed: []string{"adr_visible"}}
	uc := NewGetAddressUseCase(kr, filter)

	got, err := uc.Execute(context.Background(), "user:usr_alice", "adr_visible")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "adr_visible", got.ID)
}

// wildcard bypass → Get возвращает адрес даже без явного per-id grant'а.
func TestAddressGetPerObject_WildcardBypass(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_any")

	uc := NewGetAddressUseCase(kr, &fakeListFilter{bypass: true})
	got, err := uc.Execute(context.Background(), "user:usr_alice", "adr_any")
	require.NoError(t, err)
	assert.Equal(t, "adr_any", got.ID)
}

// fail-closed: iam-ошибка при Get enforce → Unavailable, а не ресурс.
func TestAddressGetPerObject_FailClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_x")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewGetAddressUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "adr_x")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// nil filter / empty subject → без enforce (authz делает interceptor).
func TestAddressGetPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_y")

	uc := NewGetAddressUseCase(kr, nil)
	got, err := uc.Execute(context.Background(), "user:usr_alice", "adr_y")
	require.NoError(t, err)
	assert.Equal(t, "adr_y", got.ID)
}

// No-leak (defense-in-depth): пустой subject (principal не извлечён — anon /
// gateway не проставил identity) при ВКЛЮЧЁННОМ фильтре → fail-closed (пустой
// список), НЕ unfiltered passthrough. «Не знаю, кто ты» != «доверенный system».
func TestAddressListPerObject_EmptySubjectFailsClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedAddressesLabeled(t, kr, "prj_1", "adr_a", "adr_b")

	filter := &fakeListFilter{allowed: []string{"addrs_unused"}}
	uc := NewListAddressesUseCase(kr, filter)

	addrs, _, err := uc.Execute(context.Background(), "", AddressFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, addrs, "empty subject + filter enabled -> fail-closed empty, NOT leak")
}
