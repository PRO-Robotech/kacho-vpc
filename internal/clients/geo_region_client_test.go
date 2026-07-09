// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	geov1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/geo/v1"
)

// blockingRegionClient — geov1.RegionServiceClient, чей Get блокируется до отмены
// ctx (alive-but-unresponsive geo). Без per-call дедлайна вызов висит навсегда на
// unbounded worker-ctx.
type blockingRegionClient struct{ geov1.RegionServiceClient }

func (blockingRegionClient) Get(ctx context.Context, _ *geov1.GetRegionRequest, _ ...grpc.CallOption) (*geov1.Region, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// TestGeoRegionClient_Get_PerCallDeadlineOnHungPeer — regression под audit-находку
// (concurrency): GeoRegionClient.Get обязан нести собственный per-call дедлайн.
// На unbounded ctx (worker-ctx после baggage.Extract) hung geo не должен вешать
// горутину — клиент возвращает DeadlineExceeded ~за timeout.
func TestGeoRegionClient_Get_PerCallDeadlineOnHungPeer(t *testing.T) {
	c := &GeoRegionClient{regions: blockingRegionClient{}, cache: newExistsCache(geoRegionExistsTTL), timeout: 150 * time.Millisecond}

	done := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "reg-a")
		done <- err
	}()

	select {
	case err := <-done:
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("expected DeadlineExceeded from hung geo, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return: per-call deadline not applied (goroutine hung on unbounded ctx)")
	}
}
