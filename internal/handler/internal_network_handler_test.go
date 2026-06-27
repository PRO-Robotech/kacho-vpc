// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/networkinternal"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	// blank-import регистрирует трансфер kachorepo.NetworkRecord → *vpcv1.Network
	// в DTO-реестре (тот же, что использует production-хендлер).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// fakeNetInternalRepo — узкий fake над networkinternal.NetworkRepo (Get +
// SetDefaultSGID). Для GetNetwork-тестов нужен только Get; SetDefaultSGID не
// вызывается, но обязан существовать, чтобы fake удовлетворял port-интерфейс.
// getCalls фиксирует факт обращения к репозиторию (для проверки «malformed id →
// repo НЕ вызван»).
type fakeNetInternalRepo struct {
	rec      *kachorepo.NetworkRecord
	err      error
	getCalls int
}

func (f *fakeNetInternalRepo) Get(_ context.Context, _ string) (*kachorepo.NetworkRecord, error) {
	f.getCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.rec, nil
}

func (f *fakeNetInternalRepo) SetDefaultSGID(_ context.Context, _, _ string) (*kachorepo.NetworkRecord, error) {
	return f.rec, f.err
}

// fakeSGRepo — заглушка SecurityGroupRepo; GetNetwork ее не трогает.
type fakeSGRepo struct{}

func (fakeSGRepo) Get(_ context.Context, _ string) (*kachorepo.SecurityGroupRecord, error) {
	return nil, repo.ErrNotFound
}

func newInternalNetHandler(r *fakeNetInternalRepo) *InternalNetworkHandler {
	return NewInternalNetworkHandler(networkinternal.NewService(r, fakeSGRepo{}))
}

// TestInternalNetwork_GetNetwork_MalformedID — malformed network_id → InvalidArgument
// с текстом «invalid network id '<X>'» ПЕРВЫМ стейтментом; repo НЕ вызывается.
func TestInternalNetwork_GetNetwork_MalformedID(t *testing.T) {
	r := &fakeNetInternalRepo{}
	h := newInternalNetHandler(r)

	_, err := h.GetNetwork(context.Background(), &vpcv1.GetInternalNetworkRequest{NetworkId: "garbage"})

	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid network id 'garbage'")
	assert.Equal(t, 0, r.getCalls, "repo must not be called for malformed id")
}

// TestInternalNetwork_GetNetwork_OK — валидный id → ответ с Network.Id и
// инфра-полем VrfId (которого нет в публичном Network message).
func TestInternalNetwork_GetNetwork_OK(t *testing.T) {
	id := ids.NewID(ids.PrefixNetwork)
	r := &fakeNetInternalRepo{
		rec: &kachorepo.NetworkRecord{
			Network: domain.Network{
				ID:        id,
				ProjectID: "project-1",
				Name:      domain.RcNameVPC("net-vrf"),
				VRFID:     42,
			},
		},
	}
	h := newInternalNetHandler(r)

	resp, err := h.GetNetwork(context.Background(), &vpcv1.GetInternalNetworkRequest{NetworkId: id})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetNetwork())
	assert.Equal(t, id, resp.GetNetwork().GetId())
	assert.Equal(t, "project-1", resp.GetNetwork().GetProjectId())
	assert.Equal(t, uint32(42), resp.GetVrfId(), "vrf_id must be returned as separate response field")
	assert.Equal(t, 1, r.getCalls)
}

// TestInternalNetwork_GetNetwork_NotFound — repo ErrNotFound → NotFound (через
// internalMapErr; без leak'а raw-текста).
func TestInternalNetwork_GetNetwork_NotFound(t *testing.T) {
	id := ids.NewID(ids.PrefixNetwork)
	r := &fakeNetInternalRepo{err: repo.ErrNotFound}
	h := newInternalNetHandler(r)

	_, err := h.GetNetwork(context.Background(), &vpcv1.GetInternalNetworkRequest{NetworkId: id})

	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}
