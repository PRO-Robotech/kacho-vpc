package main

// main_test.go — composition-root wiring tests.
//
// Regression guard for KAC-240: the project-level List authorization adapter
// MUST be wired over a gRPC conn to the kacho-iam **internal** listener (:9091),
// which is the ONLY listener that serves `InternalIAMService.Check`. The first
// KAC-240 cut wired it over the public ProjectService conn (`extapi.iam` :9090),
// where InternalIAMService is unregistered → every List RPC returned
// `503 list-filter unavailable: ... unknown service kacho.cloud.iam.v1.InternalIAMService`.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// fakeInternalIAMServer — minimal InternalIAMService that allows everything.
type fakeInternalIAMServer struct {
	iamv1.UnimplementedInternalIAMServiceServer
}

func (fakeInternalIAMServer) Check(context.Context, *iamv1.CheckRequest) (*iamv1.CheckResponse, error) {
	return &iamv1.CheckResponse{Allowed: true}, nil
}

// startGRPC spins up a loopback gRPC server. When withInternalIAM is true it
// registers InternalIAMService (mirrors the kacho-iam internal :9091 listener);
// when false it registers nothing (mirrors the public :9090 listener, where
// InternalIAMService is NOT served — the old buggy `iamConn` target).
func startGRPC(t *testing.T, withInternalIAM bool) grpc.ClientConnInterface {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	if withInternalIAM {
		iamv1.RegisterInternalIAMServiceServer(srv, fakeInternalIAMServer{})
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestNewListAuthz_Disabled_ReturnsNil(t *testing.T) {
	adapter, err := newListAuthz(false, nil)
	require.NoError(t, err)
	require.Nil(t, adapter)
}

func TestNewListAuthz_EnabledWithoutInternalConn_Errors(t *testing.T) {
	// list-filter enabled but no internal IAM conn (authz.iam-endpoint unset) is
	// a misconfiguration — fail fast at startup, not a silent 503 per request.
	adapter, err := newListAuthz(true, nil)
	require.Error(t, err)
	require.Nil(t, adapter)
}

func TestNewListAuthz_OverInternalListener_CanViewProject(t *testing.T) {
	conn := startGRPC(t, true) // serves InternalIAMService → :9091 listener
	adapter, err := newListAuthz(true, conn)
	require.NoError(t, err)
	require.NotNil(t, adapter)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := adapter.CanViewProject(ctx, "user:tester", "prj7afzcv99rm1n84z3a")
	require.NoError(t, err, "Check against the internal listener must succeed")
	require.True(t, ok)
}

func TestNewListAuthz_OverPublicListener_ReproducesUnknownService(t *testing.T) {
	// Regression reproduction: wiring List-authz over a conn that does NOT serve
	// InternalIAMService (the public :9090 / old `iamConn`) yields the exact
	// failure the user hit — Unimplemented "unknown service ...InternalIAMService".
	conn := startGRPC(t, false) // NO InternalIAMService → public listener
	adapter, err := newListAuthz(true, conn)
	require.NoError(t, err)
	require.NotNil(t, adapter)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = adapter.CanViewProject(ctx, "user:tester", "prj7afzcv99rm1n84z3a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "InternalIAMService",
		"a non-internal conn must fail loudly — this is why iamConn (public) was wrong")
}
