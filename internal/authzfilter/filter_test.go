// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package authzfilter

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// fakeAuthorizeClient — stub iam AuthorizeService.ListObjects.
type fakeAuthorizeClient struct {
	resp   *iamv1.ListObjectsResponse
	err    error
	calls  int
	gotReq *iamv1.ListObjectsRequest
}

func (f *fakeAuthorizeClient) ListObjects(_ context.Context, in *iamv1.ListObjectsRequest, _ ...grpc.CallOption) (*iamv1.ListObjectsResponse, error) {
	f.calls++
	f.gotReq = in
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestFGAFilter_AllowedIDsSortedAndFiltered(t *testing.T) {
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{ResourceIds: []string{"c", "a", "b"}}}
	f := NewFGAFilter(cli, DefaultConfig())

	d, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.NoError(t, err)
	assert.False(t, d.BypassAll)
	assert.False(t, d.Empty)
	assert.Equal(t, []string{"a", "b", "c"}, d.AllowedIDs, "ids must be sorted for stable pagination")
	// request shape: read==enforce (action list → viewer server-side).
	assert.Equal(t, "user:usr_x", cli.gotReq.GetSubject())
	assert.Equal(t, ResourceTypeSubnet, cli.gotReq.GetResourceType())
	assert.Equal(t, ActionSubnetList, cli.gotReq.GetAction())
}

func TestFGAFilter_WildcardGrantBypass(t *testing.T) {
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{WildcardGrant: true}}
	f := NewFGAFilter(cli, DefaultConfig())

	d, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeNetwork, ActionNetworkList)
	require.NoError(t, err)
	assert.True(t, d.BypassAll, "wildcard_grant → BypassAll (all in-scope)")
}

func TestFGAFilter_EmptyGrant(t *testing.T) {
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{ResourceIds: nil}}
	f := NewFGAFilter(cli, DefaultConfig())

	d, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.NoError(t, err)
	assert.True(t, d.Empty)
	assert.False(t, d.BypassAll)
}

func TestFGAFilter_FailClosed(t *testing.T) {
	cli := &fakeAuthorizeClient{err: status.Error(codes.Internal, "fga boom")}
	f := NewFGAFilter(cli, DefaultConfig()) // FailOpen=false

	_, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code(), "fail-closed maps FGA error → Unavailable")
}

func TestFGAFilter_FailOpenBypass(t *testing.T) {
	cli := &fakeAuthorizeClient{err: errors.New("boom")}
	cfg := DefaultConfig()
	cfg.FailOpen = true
	f := NewFGAFilter(cli, cfg)

	d, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.NoError(t, err)
	assert.True(t, d.BypassAll)
	assert.True(t, d.FailOpen)
}

func TestFGAFilter_AnonymousFailClosed(t *testing.T) {
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{}}
	f := NewFGAFilter(cli, DefaultConfig())

	_, err := f.ListAllowedIDs(context.Background(), "", ResourceTypeSubnet, ActionSubnetList)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Equal(t, 0, cli.calls, "anonymous → no FGA call")
}

func TestFGAFilter_Cache(t *testing.T) {
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{ResourceIds: []string{"a"}}}
	f := NewFGAFilter(cli, DefaultConfig())

	_, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.NoError(t, err)
	d2, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.NoError(t, err)
	assert.Equal(t, 1, cli.calls, "second identical call served from cache")
	assert.True(t, d2.FromCache)
}

func TestFGAFilter_DisabledOrNilClientBypass(t *testing.T) {
	// nil client → bypass (graceful start).
	f := NewFGAFilter(nil, DefaultConfig())
	d, err := f.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.NoError(t, err)
	assert.True(t, d.BypassAll)

	// Enabled=false → bypass.
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{}}
	cfg := DefaultConfig()
	cfg.Enabled = false
	f2 := NewFGAFilter(cli, cfg)
	d2, err := f2.ListAllowedIDs(context.Background(), "user:usr_x", ResourceTypeSubnet, ActionSubnetList)
	require.NoError(t, err)
	assert.True(t, d2.BypassAll)
	assert.Equal(t, 0, cli.calls)
}

// AsPort + EnforceVisible (Get no-leak helper).
func TestAsPortAndEnforceVisible(t *testing.T) {
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{ResourceIds: []string{"e9b_a", "e9b_b"}}}
	port := AsPort(NewFGAFilter(cli, DefaultConfig()))
	require.NotNil(t, port)

	// id in set → visible.
	ok, err := EnforceVisible(context.Background(), port, "user:usr_x", ResourceTypeSubnet, ActionSubnetList, "e9b_a")
	require.NoError(t, err)
	assert.True(t, ok)

	// id not in set → not visible (caller → NotFound).
	ok, err = EnforceVisible(context.Background(), port, "user:usr_x", ResourceTypeSubnet, ActionSubnetList, "e9b_z")
	require.NoError(t, err)
	assert.False(t, ok)

	// nil port → passthrough visible.
	ok, err = EnforceVisible(context.Background(), nil, "user:usr_x", ResourceTypeSubnet, ActionSubnetList, "e9b_z")
	require.NoError(t, err)
	assert.True(t, ok)

	// empty subject → passthrough visible.
	ok, err = EnforceVisible(context.Background(), port, "", ResourceTypeSubnet, ActionSubnetList, "e9b_z")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestAsPort_NilFilterReturnsNil(t *testing.T) {
	assert.Nil(t, AsPort(nil))
	var typedNil *FGAFilter
	assert.Nil(t, AsPort(typedNil), "typed-nil *FGAFilter → nil UseCasePort (passthrough)")
}

func TestEnforceVisible_FailClosed(t *testing.T) {
	cli := &fakeAuthorizeClient{err: status.Error(codes.Internal, "boom")}
	port := AsPort(NewFGAFilter(cli, DefaultConfig()))
	ok, err := EnforceVisible(context.Background(), port, "user:usr_x", ResourceTypeSubnet, ActionSubnetList, "e9b_a")
	require.Error(t, err)
	assert.False(t, ok)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestEnforceVisible_WildcardVisible(t *testing.T) {
	cli := &fakeAuthorizeClient{resp: &iamv1.ListObjectsResponse{WildcardGrant: true}}
	port := AsPort(NewFGAFilter(cli, DefaultConfig()))
	ok, err := EnforceVisible(context.Background(), port, "user:usr_x", ResourceTypeSubnet, ActionSubnetList, "anything")
	require.NoError(t, err)
	assert.True(t, ok, "wildcard grant → Get visible for any id")
}
