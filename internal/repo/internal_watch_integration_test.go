package repo_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/outbox"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
)

// --- fake server-stream collecting sent events --------------------------------

// fakeWatchStream implements vpcv1.InternalWatchService_WatchServer
// (= grpc.ServerStreamingServer[vpcv1.Event]) by appending Send'ed events into
// an internal slice. Thread-safe.
type fakeWatchStream struct {
	ctx    context.Context
	mu     sync.Mutex
	events []*vpcv1.Event
}

func newFakeWatchStream(ctx context.Context) *fakeWatchStream {
	return &fakeWatchStream{ctx: ctx}
}

func (f *fakeWatchStream) Send(e *vpcv1.Event) error {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
	return nil
}

func (f *fakeWatchStream) snapshot() []*vpcv1.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*vpcv1.Event, len(f.events))
	copy(out, f.events)
	return out
}

func (f *fakeWatchStream) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// grpc.ServerStream surface — only Context() is meaningfully exercised.
func (f *fakeWatchStream) Context() context.Context     { return f.ctx }
func (f *fakeWatchStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeWatchStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeWatchStream) SetTrailer(metadata.MD)       {}
func (f *fakeWatchStream) SendMsg(m any) error          { return nil }
func (f *fakeWatchStream) RecvMsg(m any) error          { return nil }

var _ grpc.ServerStreamingServer[vpcv1.Event] = (*fakeWatchStream)(nil)

// --- helpers ------------------------------------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// emitOutbox writes one row into vpc_outbox via the outbox helper (trigger
// vpc_outbox_notify_trg fires pg_notify('vpc_outbox', sequence_no::text)).
func emitOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind, id, eventType string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, outbox.Emit(ctx, tx, "vpc_outbox", kind, id, eventType, map[string]any{"id": id}))
	require.NoError(t, tx.Commit(ctx))
}

// maxSeq returns the current MAX(sequence_no) in vpc_outbox (0 if empty).
func maxSeq(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var n *int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT max(sequence_no) FROM vpc_outbox`).Scan(&n))
	if n == nil {
		return 0
	}
	return *n
}

// startWatch runs handler.Watch in a goroutine; returns the fake stream, a
// cancel func to terminate the stream, and an error channel that receives the
// Watch return value once it exits.
func startWatch(h *handler.InternalWatchHandler, from int64) (*fakeWatchStream, context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeWatchStream(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.Watch(&vpcv1.WatchRequest{FromSequenceNo: from}, stream)
	}()
	return stream, cancel, errCh
}

// --- the test -----------------------------------------------------------------

func TestIntegration_InternalWatch_CatchupNotifyResumeCap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// --- (a) catchup: pre-insert N outbox rows, Watch(from=0) -> receive all N in order
	const n = 5
	for i := 0; i < n; i++ {
		emitOutbox(t, ctx, pool, "Network", "enp-catchup", "CREATED")
	}
	lastAfterCatchup := maxSeq(t, ctx, pool)
	require.EqualValues(t, n, lastAfterCatchup)

	h := handler.NewInternalWatchHandler(pool, dsn, discardLogger(), 32)

	stream, cancel, errCh := startWatch(h, 0)
	require.Eventually(t, func() bool { return stream.len() >= n }, 10*time.Second, 20*time.Millisecond,
		"catchup must stream all %d pre-inserted events", n)

	got := stream.snapshot()
	require.Len(t, got, n)
	for i := 1; i < len(got); i++ {
		assert.Greater(t, got[i].SequenceNo, got[i-1].SequenceNo, "events must arrive in ascending sequence order")
	}
	assert.EqualValues(t, n, got[n-1].SequenceNo)
	assert.Equal(t, "Network", got[0].ResourceKind)
	assert.Equal(t, "CREATED", got[0].EventType)

	// --- (b) live notify: while the same stream is alive, emit a new event -> it is streamed
	emitOutbox(t, ctx, pool, "Subnet", "e9b-live", "UPDATED")
	require.Eventually(t, func() bool { return stream.len() >= n+1 }, 10*time.Second, 20*time.Millisecond,
		"live NOTIFY must deliver the freshly-emitted event")
	live := stream.snapshot()[n]
	assert.Equal(t, "Subnet", live.ResourceKind)
	assert.Equal(t, "UPDATED", live.EventType)
	assert.Greater(t, live.SequenceNo, lastAfterCatchup)

	cancel()
	select {
	case werr := <-errCh:
		assert.NoError(t, werr, "client cancel must be a graceful (nil) exit")
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return after context cancel")
	}

	// --- (c) resume: Watch(from=K) -> only events with sequence_no > K
	// Emit a couple more rows; resume from the cursor at the boundary.
	resumeFrom := maxSeq(t, ctx, pool) // = n+1 (catchup + live event)
	emitOutbox(t, ctx, pool, "Address", "e9b-resume-1", "CREATED")
	emitOutbox(t, ctx, pool, "RouteTable", "enp-resume-2", "DELETED")
	wantResume := maxSeq(t, ctx, pool) - resumeFrom // expect exactly 2 new events

	stream2, cancel2, errCh2 := startWatch(h, resumeFrom)
	defer cancel2()
	require.Eventually(t, func() bool { return stream2.len() >= int(wantResume) }, 10*time.Second, 20*time.Millisecond,
		"resume must replay only events after cursor %d", resumeFrom)
	for _, e := range stream2.snapshot() {
		assert.Greater(t, e.SequenceNo, resumeFrom, "resumed stream must not replay events at-or-before the cursor")
	}
	// Give the stream a brief moment to ensure no extra (stale) events leak in.
	time.Sleep(200 * time.Millisecond)
	assert.EqualValues(t, wantResume, stream2.len(), "resume must deliver exactly the events after the cursor")

	cancel2()
	select {
	case <-errCh2:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch (resume) did not return after context cancel")
	}

	// --- (d) stream cap: handler with WatchMaxStreams=2 -> the 3rd Watch returns ResourceExhausted
	// There are already (n+3) events in vpc_outbox; the two streams catch up from
	// 0, so once both have streamed something we know they hold their slots.
	hCapped := handler.NewInternalWatchHandler(pool, dsn, discardLogger(), 2)
	s1, c1, e1 := startWatch(hCapped, 0)
	s2, c2, e2 := startWatch(hCapped, 0)
	defer c1()
	defer c2()
	require.Eventually(t, func() bool { return s1.len() > 0 && s2.len() > 0 }, 10*time.Second, 20*time.Millisecond,
		"both capped streams must acquire their slot and catch up")
	// Both should still be running (no error yet).
	select {
	case err := <-e1:
		t.Fatalf("stream 1 exited unexpectedly: %v", err)
	case err := <-e2:
		t.Fatalf("stream 2 exited unexpectedly: %v", err)
	case <-time.After(300 * time.Millisecond):
		// expected: both alive
	}
	// The 3rd concurrent Watch must be rejected immediately.
	s3 := newFakeWatchStream(context.Background())
	err3 := hCapped.Watch(&vpcv1.WatchRequest{FromSequenceNo: 0}, s3)
	require.Error(t, err3)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err3))
	assert.Zero(t, s3.len(), "rejected stream must not have streamed anything")

	// After freeing a slot, a new Watch should be accepted again.
	c1()
	select {
	case <-e1:
	case <-time.After(5 * time.Second):
		t.Fatal("capped stream 1 did not return after cancel")
	}
	s4, c4, e4 := startWatch(hCapped, 0)
	defer c4()
	require.Eventually(t, func() bool { return s4.len() > 0 }, 10*time.Second, 20*time.Millisecond,
		"a new Watch must be accepted after a slot is freed")
	select {
	case err := <-e4:
		t.Fatalf("stream 4 (after slot freed) exited unexpectedly: %v", err)
	case <-time.After(300 * time.Millisecond):
		// expected: alive, slot was reusable
	}
}
