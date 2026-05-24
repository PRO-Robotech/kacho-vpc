package listauthz

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
)

type fakeListObjects struct {
	calls int
	resp  authz.ListObjectsResponse
	err   error
}

func (f *fakeListObjects) ListObjects(_ context.Context, _ authz.ListObjectsRequest) (authz.ListObjectsResponse, error) {
	f.calls++
	if f.err != nil {
		return authz.ListObjectsResponse{}, f.err
	}
	return f.resp, nil
}

func TestAdapter_NilSvcReturnsUnavailable(t *testing.T) {
	var a *Adapter = nil
	_, err := a.ListAllowedIDs(context.Background(), "user:x", "vpc_network", "act", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, authz.ErrUnavailable)
}

func TestAdapter_NilInnerSvcReturnsUnavailable(t *testing.T) {
	a := New(nil)
	_, err := a.ListAllowedIDs(context.Background(), "user:x", "vpc_network", "act", "")
	assert.ErrorIs(t, err, authz.ErrUnavailable)
}

func TestAdapter_PassesThroughToService(t *testing.T) {
	fake := &fakeListObjects{resp: authz.ListObjectsResponse{ResourceIDs: []string{"id-1"}}}
	svc := authz.NewListObjectsService(fake, authz.ListObjectsConfig{TTL: time.Second})
	a := New(svc)

	ids, err := a.ListAllowedIDs(context.Background(), "user:x", "vpc_network", "act", "prj_1")
	require.NoError(t, err)
	assert.Equal(t, []string{"id-1"}, ids)
	assert.Equal(t, 1, fake.calls)
}

func TestAdapter_ScopeHintCacheSeparation(t *testing.T) {
	fake := &fakeListObjects{resp: authz.ListObjectsResponse{ResourceIDs: []string{"id-1"}}}
	svc := authz.NewListObjectsService(fake, authz.ListObjectsConfig{TTL: time.Second})
	a := New(svc)

	_, _ = a.ListAllowedIDs(context.Background(), "user:x", "vpc_network", "act", "prj_1")
	_, _ = a.ListAllowedIDs(context.Background(), "user:x", "vpc_network", "act", "prj_2")
	assert.Equal(t, 2, fake.calls, "different scope hints should bypass cache")
}

func TestAdapter_ErrorPropagation(t *testing.T) {
	fake := &fakeListObjects{err: errors.New("boom")}
	svc := authz.NewListObjectsService(fake, authz.ListObjectsConfig{TTL: time.Second})
	a := New(svc)

	_, err := a.ListAllowedIDs(context.Background(), "user:x", "vpc_network", "act", "")
	assert.ErrorIs(t, err, authz.ErrUnavailable)
}

func TestAsPort_NilAdapter(t *testing.T) {
	p := AsPort(nil)
	assert.Nil(t, p, "nil Adapter → nil Port")
}

func TestAsPort_NilSvc(t *testing.T) {
	a := New(nil)
	p := AsPort(a)
	assert.Nil(t, p, "Adapter with nil svc → nil Port")
}

func TestAsPort_ValidAdapter(t *testing.T) {
	fake := &fakeListObjects{}
	svc := authz.NewListObjectsService(fake, authz.ListObjectsConfig{TTL: time.Second})
	a := New(svc)
	p := AsPort(a)
	assert.NotNil(t, p)
}

// KAC-178 §1: MapListFilterErr должен разделять PermissionDenied → 403
// и всё остальное → 503 (legacy behaviour для infra errors). До этого
// helper'а use-case'ы блиндово wrap'или любой error в Unavailable.
func TestMapListFilterErr_PermissionDenied_ReturnsPermissionDenied(t *testing.T) {
	// corelib теперь возвращает PermissionDenied wrap'нутый в ErrPermissionDenied.
	innerErr := fmt.Errorf("%w: rpc error: code = PermissionDenied desc = permission denied", authz.ErrPermissionDenied)

	mapped := MapListFilterErr(innerErr)
	require.Error(t, mapped)
	assert.Equal(t, codes.PermissionDenied, status.Code(mapped),
		"PermissionDenied sentinel должен мапиться на gRPC PermissionDenied (HTTP 403), а не Unavailable")
	assert.Equal(t, "list-filter denied", status.Convert(mapped).Message())
}

func TestMapListFilterErr_Unavailable_ReturnsUnavailable(t *testing.T) {
	innerErr := fmt.Errorf("%w: connection refused", authz.ErrUnavailable)

	mapped := MapListFilterErr(innerErr)
	require.Error(t, mapped)
	assert.Equal(t, codes.Unavailable, status.Code(mapped))
	assert.Contains(t, status.Convert(mapped).Message(), "list-filter unavailable")
	assert.Contains(t, status.Convert(mapped).Message(), "connection refused")
}

func TestMapListFilterErr_NilReturnsNil(t *testing.T) {
	assert.Nil(t, MapListFilterErr(nil))
}

func TestMapListFilterErr_GenericErrorTreatedAsUnavailable(t *testing.T) {
	// Errors без ErrPermissionDenied wrap'а — fallback на Unavailable
	// (defensive: даже если caller передал не-corelib error).
	mapped := MapListFilterErr(errors.New("boom"))
	require.Error(t, mapped)
	assert.Equal(t, codes.Unavailable, status.Code(mapped))
}
