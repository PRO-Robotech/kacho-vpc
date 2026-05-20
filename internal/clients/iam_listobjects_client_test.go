package clients

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// fakeAuthorizeService — in-memory gRPC server для тестов adapter'а.
type fakeAuthorizeService struct {
	iamv1.UnimplementedAuthorizeServiceServer

	resp        *iamv1.ListObjectsResponse
	err         error
	lastReq     *iamv1.ListObjectsRequest
	lastModelID string
}

func (f *fakeAuthorizeService) ListObjects(ctx context.Context, req *iamv1.ListObjectsRequest) (*iamv1.ListObjectsResponse, error) {
	f.lastReq = req
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vs := md.Get("x-kacho-authz-model-id"); len(vs) > 0 {
			f.lastModelID = vs[0]
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// startFakeIAM поднимает gRPC server in-memory bufconn-style.
func startFakeIAM(t *testing.T, fake *fakeAuthorizeService) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	iamv1.RegisterAuthorizeServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.GracefulStop() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestIAMListObjectsClient_HappyPath(t *testing.T) {
	fake := &fakeAuthorizeService{
		resp: &iamv1.ListObjectsResponse{
			ResourceIds:   []string{"enp-1", "enp-2"},
			NextPageToken: "",
			Truncated:     false,
		},
	}
	conn := startFakeIAM(t, fake)
	client := NewIAMListObjectsClient(conn)
	require.NotNil(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.ListObjects(ctx, authz.ListObjectsRequest{
		Subject:      "user:usr_alice",
		ResourceType: "vpc_network",
		Action:       "vpc.networks.list",
		MaxResults:   100,
		AuthzModelID: "m_v3",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"enp-1", "enp-2"}, resp.ResourceIDs)
	assert.Empty(t, resp.NextPageToken)
	assert.False(t, resp.Truncated)

	// Verify request fields transmitted correctly.
	require.NotNil(t, fake.lastReq)
	assert.Equal(t, "user:usr_alice", fake.lastReq.Subject)
	assert.Equal(t, "vpc_network", fake.lastReq.ResourceType)
	assert.Equal(t, "vpc.networks.list", fake.lastReq.Action)
	assert.Equal(t, int64(100), fake.lastReq.MaxResults)
	// AuthzModelID passed via gRPC metadata.
	assert.Equal(t, "m_v3", fake.lastModelID)
}

func TestIAMListObjectsClient_GRPCErrorPropagates(t *testing.T) {
	fake := &fakeAuthorizeService{err: status.Error(codes.Internal, "FGA cluster down")}
	conn := startFakeIAM(t, fake)
	client := NewIAMListObjectsClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.ListObjects(ctx, authz.ListObjectsRequest{
		Subject: "user:x", ResourceType: "vpc_network", Action: "act",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestIAMListObjectsClient_NilConnGivesNilClient(t *testing.T) {
	client := NewIAMListObjectsClient(nil)
	assert.Nil(t, client)
}

func TestIAMListObjectsClient_NilReceiverErrors(t *testing.T) {
	var client *IAMListObjectsClient = nil
	_, err := client.ListObjects(context.Background(), authz.ListObjectsRequest{})
	require.Error(t, err)
}

func TestIAMListObjectsClient_EmptyModelIDOmitsHeader(t *testing.T) {
	fake := &fakeAuthorizeService{resp: &iamv1.ListObjectsResponse{}}
	conn := startFakeIAM(t, fake)
	client := NewIAMListObjectsClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.ListObjects(ctx, authz.ListObjectsRequest{
		Subject: "user:x", ResourceType: "vpc_network", Action: "act",
		// AuthzModelID intentionally empty
	})
	require.NoError(t, err)
	assert.Empty(t, fake.lastModelID, "empty AuthzModelID should not emit header")
}
