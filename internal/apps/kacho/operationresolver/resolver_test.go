// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package operationresolver_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/operations"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/operationresolver"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// Resolver сверяет осиротевшую операцию с committed-реальностью ресурса:
// Create/Update present → Done(current); absent → Interrupted. Delete наоборот.
// Неузнанный тип метаданных → Skip. Fake kachoRepo — kachomock.

func mustAny(t *testing.T, m proto.Message) *anypb.Any {
	t.Helper()
	a, err := anypb.New(m)
	require.NoError(t, err)
	return a
}

func newOp(meta *anypb.Any) operations.Operation {
	return operations.Operation{ID: "opn-1", Metadata: meta}
}

func seedNetwork(t *testing.T, repo *kachomock.Repository, id string) {
	t.Helper()
	ctx := context.Background()
	w, err := repo.Writer(ctx)
	require.NoError(t, err)
	_, err = w.Networks().Insert(ctx, &domain.Network{ID: id, ProjectID: "prj-1", Name: domain.RcNameVPC("n")})
	require.NoError(t, err)
	require.NoError(t, w.Commit())
}

func TestResolver_CreateNetwork_Present_Done(t *testing.T) {
	repo := kachomock.NewRepository()
	seedNetwork(t, repo, "enp-present")
	r := operationresolver.New(repo)

	res, err := r.Resolve(context.Background(), newOp(mustAny(t, &vpcv1.CreateNetworkMetadata{NetworkId: "enp-present"})))
	require.NoError(t, err)
	require.Equal(t, operations.OutcomeDone, res.Outcome)
	require.NotNil(t, res.Response, "Done на Create несет текущий ресурс как Response")
}

func TestResolver_CreateNetwork_Absent_Interrupted(t *testing.T) {
	repo := kachomock.NewRepository()
	r := operationresolver.New(repo)

	res, err := r.Resolve(context.Background(), newOp(mustAny(t, &vpcv1.CreateNetworkMetadata{NetworkId: "enp-missing"})))
	require.NoError(t, err)
	require.Equal(t, operations.OutcomeInterrupted, res.Outcome)
	require.Nil(t, res.Response)
}

func TestResolver_UpdateNetwork_Present_Done(t *testing.T) {
	repo := kachomock.NewRepository()
	seedNetwork(t, repo, "enp-upd")
	r := operationresolver.New(repo)

	res, err := r.Resolve(context.Background(), newOp(mustAny(t, &vpcv1.UpdateNetworkMetadata{NetworkId: "enp-upd"})))
	require.NoError(t, err)
	require.Equal(t, operations.OutcomeDone, res.Outcome)
	require.NotNil(t, res.Response)
}

func TestResolver_DeleteNetwork_Absent_Done(t *testing.T) {
	repo := kachomock.NewRepository()
	r := operationresolver.New(repo)

	res, err := r.Resolve(context.Background(), newOp(mustAny(t, &vpcv1.DeleteNetworkMetadata{NetworkId: "enp-gone"})))
	require.NoError(t, err)
	require.Equal(t, operations.OutcomeDone, res.Outcome)
	require.Nil(t, res.Response, "Delete-done несет пустой response (Empty-семантика)")
}

func TestResolver_DeleteNetwork_Present_Interrupted(t *testing.T) {
	repo := kachomock.NewRepository()
	seedNetwork(t, repo, "enp-still-here")
	r := operationresolver.New(repo)

	res, err := r.Resolve(context.Background(), newOp(mustAny(t, &vpcv1.DeleteNetworkMetadata{NetworkId: "enp-still-here"})))
	require.NoError(t, err)
	require.Equal(t, operations.OutcomeInterrupted, res.Outcome)
}

func TestResolver_UnknownMetadata_Skip(t *testing.T) {
	repo := kachomock.NewRepository()
	r := operationresolver.New(repo)

	res, err := r.Resolve(context.Background(), newOp(mustAny(t, &emptypb.Empty{})))
	require.NoError(t, err)
	require.Equal(t, operations.OutcomeSkip, res.Outcome)
}

func TestResolver_NilMetadata_Skip(t *testing.T) {
	repo := kachomock.NewRepository()
	r := operationresolver.New(repo)

	res, err := r.Resolve(context.Background(), newOp(nil))
	require.NoError(t, err)
	require.Equal(t, operations.OutcomeSkip, res.Outcome)
}
