// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// Per-object filtered List для NetworkService.List. Контракт тот же, что у
// subnet — возвращать только авторизованные subject'у networks, read==enforce,
// fail-closed, wildcard → все ресурсы в scope.

type fakeNetListFilter struct {
	allowed []string
	bypass  bool
	err     error

	gotResourceType string
	gotAction       string
	calls           int
}

func (f *fakeNetListFilter) ListAllowedIDs(_ context.Context, _, resourceType, action string) ([]string, bool, error) {
	f.calls++
	f.gotResourceType = resourceType
	f.gotAction = action
	if f.err != nil {
		return nil, false, f.err
	}
	return f.allowed, f.bypass, nil
}

func seedNetworksLabeled(t *testing.T, kr *kachomock.Repository, projectID string, netIDs ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range netIDs {
		n := &domain.Network{ID: id, ProjectID: projectID, Name: domain.RcNameVPC("net-" + id)}
		if _, ierr := w.Networks().Insert(context.Background(), n); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// scope_grant → все networks в scope; cross-account networks отсутствуют
// (это обеспечивает FGA-containment, здесь представлен через allowed-set /
// bypass). С явным allowed-набором возвращаются только перечисленные id.
func TestNetworkListPerObject_ReturnsOnlyAllowed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworksLabeled(t, kr, "prj_1", "net_a", "net_b", "net_c")

	filter := &fakeNetListFilter{allowed: []string{"net_a", "net_c"}}
	uc := NewListNetworksUseCase(kr, filter)

	nets, _, err := uc.Execute(context.Background(), "user:usr_bob", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nets, 2)
	got := map[string]bool{}
	for _, n := range nets {
		got[n.ID] = true
	}
	assert.True(t, got["net_a"])
	assert.True(t, got["net_c"])
	assert.False(t, got["net_b"])

	assert.Equal(t, "vpc_network", filter.gotResourceType)
	assert.Equal(t, "vpc.networks.list", filter.gotAction)
}

// Wildcard bypass → все строки проекта.
func TestNetworkListPerObject_WildcardBypassReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworksLabeled(t, kr, "prj_1", "net_a", "net_b")

	uc := NewListNetworksUseCase(kr, &fakeNetListFilter{bypass: true})
	nets, _, err := uc.Execute(context.Background(), "user:usr_bob", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nets, 2)
}

// No-leak (defense-in-depth): пустой subject (principal не извлечен — anon /
// gateway не проставил identity) при ВКЛЮЧЕННОМ фильтре НЕ должен давать
// unfiltered passthrough (leak всех networks проекта). Пустой subject — это
// «не знаю, кто ты» → fail-closed (пустой список), НЕ «доверенный system-вызов».
func TestNetworkListPerObject_EmptySubjectFailsClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworksLabeled(t, kr, "prj_1", "net_secret1", "net_secret2")

	filter := &fakeNetListFilter{allowed: []string{"net_secret1"}}
	uc := NewListNetworksUseCase(kr, filter)

	nets, _, err := uc.Execute(context.Background(), "", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, nets, "empty subject + filter enabled → fail-closed empty, NOT unfiltered passthrough (leak)")
}

// No-leak: пустой grant → пустой список.
func TestNetworkListPerObject_EmptyGrantEmpty(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworksLabeled(t, kr, "prj_1", "net_secret")

	uc := NewListNetworksUseCase(kr, &fakeNetListFilter{allowed: nil})
	nets, _, err := uc.Execute(context.Background(), "user:usr_bob", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, nets)
}

// Fail-closed: infra недоступна → Unavailable.
func TestNetworkListPerObject_FailClosed(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworksLabeled(t, kr, "prj_1", "net_a")

	uc := NewListNetworksUseCase(kr, &fakeNetListFilter{err: status.Error(codes.Unavailable, "iam down")})
	_, _, err := uc.Execute(context.Background(), "user:usr_bob", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// Пустой subject → passthrough, без вызова FGA.
// Явный доверенный system-вызов (authzfilter.SystemSubject) → unfiltered
// passthrough (полный список, фильтр не зовется). Отличается от пустого subject
// (anon, fail-closed): system несет явный sentinel.
func TestNetworkListPerObject_SystemSubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworksLabeled(t, kr, "prj_1", "net_a", "net_b")

	filter := &fakeNetListFilter{allowed: []string{"net_a"}}
	uc := NewListNetworksUseCase(kr, filter)
	nets, _, err := uc.Execute(context.Background(), authzfilter.SystemSubject, NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nets, 2)
	assert.Equal(t, 0, filter.calls)
}
