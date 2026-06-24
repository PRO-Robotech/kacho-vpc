// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

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

// Тесты per-object фильтрации List и no-leak Get для NetworkInterfaceService:
// возвращаем ТОЛЬКО разрешенные subject'у NIC'и (relation viewer, FGA type
// vpc_network_interface), read==enforce, fail-closed при недоступном iam,
// wildcard scope_grant → весь scope, пустой grant → пусто (no-leak).

// fakeListFilter — in-memory ListFilter для unit-тестов: запоминает аргументы
// (subject, resourceType, action), с которыми его позвали, и возвращает заданное
// решение.
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

// seedNICsLabeled — вставляет NIC'и с заданными id в project/subnet.
func seedNICsLabeled(t *testing.T, kr *kachomock.Repository, projectID, subnetID string, nicIDs ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range nicIDs {
		n := &domain.NetworkInterface{
			ID:        id,
			ProjectID: projectID,
			Name:      domain.RcNameVPC("nic-" + id),
			SubnetID:  subnetID,
			Status:    domain.NIStatusAvailable,
		}
		if _, ierr := w.NetworkInterfaces().Insert(context.Background(), n); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// List возвращает ровно per-object разрешенный набор.
func TestNetworkInterfaceListPerObject_ReturnsOnlyAllowed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_aaa", "nic_bbb", "nic_ccc")

	filter := &fakeListFilter{allowed: []string{"nic_aaa", "nic_bbb"}}
	uc := NewListNetworkInterfacesUseCase(kr, filter)

	nics, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nics, 2)
	got := map[string]bool{}
	for _, n := range nics {
		got[n.ID] = true
	}
	assert.True(t, got["nic_aaa"])
	assert.True(t, got["nic_bbb"])
	assert.False(t, got["nic_ccc"], "nic_ccc not in the allowed set → must not appear")

	// read==enforce: фильтр зовется с read-verb, смапленным на viewer
	// (action vpc.networkInterfaces.list, FGA type vpc_network_interface).
	assert.Equal(t, "user:usr_alice", filter.gotSubject)
	assert.Equal(t, "vpc_network_interface", filter.gotResourceType)
	assert.Equal(t, "vpc.networkInterfaces.list", filter.gotAction)
}

// no-leak: объект вне всех grant'ов отсутствует в List.
func TestNetworkInterfaceListPerObject_NoLeak(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_visible", "nic_secret")

	filter := &fakeListFilter{allowed: []string{"nic_visible"}}
	uc := NewListNetworkInterfacesUseCase(kr, filter)

	nics, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nics, 1)
	assert.Equal(t, "nic_visible", nics[0].ID)
}

// empty grant: subject без grant'а → пустой список (НЕ нефильтрованный).
func TestNetworkInterfaceListPerObject_EmptyGrantEmptyList(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_a", "nic_b")

	filter := &fakeListFilter{allowed: nil}
	uc := NewListNetworkInterfacesUseCase(kr, filter)

	nics, next, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, nics)
	assert.Empty(t, next)
}

// wildcard scope_grant → весь scope. Фильтр возвращает bypass=true, use-case
// отдает все строки в пределах project.
func TestNetworkInterfaceListPerObject_WildcardBypassReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_a", "nic_b", "nic_c")

	filter := &fakeListFilter{bypass: true}
	uc := NewListNetworkInterfacesUseCase(kr, filter)

	nics, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nics, 3)
}

// fail-closed: iam недоступен → Unavailable (НЕ нефильтрованный, не молча пустой).
func TestNetworkInterfaceListPerObject_FailClosedUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_a")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewListNetworkInterfacesUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// fail-closed (plain error): не-status ошибка тоже маппится в non-OK код, никогда
// не проходит молча нефильтрованной.
func TestNetworkInterfaceListPerObject_FailClosedPlainError(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_a")

	filter := &fakeListFilter{err: errors.New("boom")}
	uc := NewListNetworkInterfacesUseCase(kr, filter)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.NotEqual(t, codes.OK, st.Code())
}

// nil filter → нефильтрованный passthrough (list-filter отключен).
func TestNetworkInterfaceListPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_a", "nic_b")

	uc := NewListNetworkInterfacesUseCase(kr, nil)
	nics, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nics, 2)
}

// пустой subject (system principal) → нефильтрованный passthrough, без FGA-вызова.
func TestNetworkInterfaceListPerObject_EmptySubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_a", "nic_b")

	filter := &fakeListFilter{allowed: []string{"nic_a"}}
	uc := NewListNetworkInterfacesUseCase(kr, filter)

	nics, _, err := uc.Execute(context.Background(), "", NetworkInterfaceFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nics, 2)
	assert.Equal(t, 0, filter.calls, "system principal must not trigger an FGA ListObjects call")
}

// project_id по-прежнему обязателен (контракт не меняется).
func TestNetworkInterfaceListPerObject_ProjectIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewListNetworkInterfacesUseCase(kr, &fakeListFilter{bypass: true})

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkInterfaceFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// no-leak Get: subject без grant'а на существующий NIC → NotFound (не
// PermissionDenied), с тем же текстом, что и для несуществующего NIC.
func TestNetworkInterfaceGetPerObject_NoLeakNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_hidden")

	// subject'у выдан grant на другой NIC, НЕ на nic_hidden.
	filter := &fakeListFilter{allowed: []string{"nic_other"}}
	uc := NewGetNetworkInterfaceUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "nic_hidden")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code(), "ungranted existing NIC → NotFound, not PermissionDenied")
	assert.Contains(t, st.Message(), "not found")
}

// read==enforce: subject'у выдан grant на NIC → Get его возвращает.
func TestNetworkInterfaceGetPerObject_GrantedReturnsResource(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_visible")

	filter := &fakeListFilter{allowed: []string{"nic_visible"}}
	uc := NewGetNetworkInterfaceUseCase(kr, filter)

	got, err := uc.Execute(context.Background(), "user:usr_alice", "nic_visible")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "nic_visible", got.ID)
}

// wildcard bypass → Get возвращает NIC даже без явного per-id grant'а.
func TestNetworkInterfaceGetPerObject_WildcardBypass(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_any")

	uc := NewGetNetworkInterfaceUseCase(kr, &fakeListFilter{bypass: true})
	got, err := uc.Execute(context.Background(), "user:usr_alice", "nic_any")
	require.NoError(t, err)
	assert.Equal(t, "nic_any", got.ID)
}

// fail-closed: ошибка iam при enforce на Get → Unavailable, а не ресурс.
func TestNetworkInterfaceGetPerObject_FailClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_x")

	filter := &fakeListFilter{err: status.Error(codes.Unavailable, "iam down")}
	uc := NewGetNetworkInterfaceUseCase(kr, filter)

	_, err := uc.Execute(context.Background(), "user:usr_alice", "nic_x")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// nil filter / пустой subject → enforce не выполняется (authz делает interceptor).
func TestNetworkInterfaceGetPerObject_NilFilterPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNICsLabeled(t, kr, "prj_1", "sub_net1", "nic_y")

	uc := NewGetNetworkInterfaceUseCase(kr, nil)
	got, err := uc.Execute(context.Background(), "user:usr_alice", "nic_y")
	require.NoError(t, err)
	assert.Equal(t, "nic_y", got.ID)
}
