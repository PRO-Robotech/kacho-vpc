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

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// blockingRegionClient — geov1.RegionServiceClient, чей Get блокируется до отмены
// ctx (alive-but-unresponsive geo). Без per-call дедлайна вызов висит навсегда на
// unbounded worker-ctx.
type blockingRegionClient struct{ geov1.RegionServiceClient }

func (blockingRegionClient) Get(ctx context.Context, _ *geov1.GetRegionRequest, _ ...grpc.CallOption) (*geov1.Region, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// fakeGeoRegionClient — детерминированный stub geov1.RegionServiceClient под
// unit-тесты GeoRegionClient. Реализует только Get (остальное — из embedded nil-
// интерфейса, не вызывается).
type fakeGeoRegionClient struct {
	geov1.RegionServiceClient
	getResp  *geov1.Region
	getCalls int
}

func (f *fakeGeoRegionClient) Get(_ context.Context, _ *geov1.GetRegionRequest, _ ...grpc.CallOption) (*geov1.Region, error) {
	f.getCalls++
	return f.getResp, nil
}

// newTestGeoRegionClient собирает GeoRegionClient поверх fake RegionServiceClient,
// инъектируя stub в обход gRPC-conn (unit-уровень, без сети).
func newTestGeoRegionClient(fake geov1.RegionServiceClient) *GeoRegionClient {
	return &GeoRegionClient{regions: fake, cache: newValueCache[*domain.Region](geoRegionExistsTTL), timeout: defaultPeerCallTimeout}
}

// TestGeoRegionClient_Get_CacheHitReturnsFullStruct — regression под audit-находку
// (readability): положительный cache-hit обязан вернуть ТУ ЖЕ полную проекцию
// региона (ID+Name), что и cache-miss, а не усечённый {ID}. Иначе caller,
// читающий .Name, молча получает ” на весь TTL — latent foot-gun.
func TestGeoRegionClient_Get_CacheHitReturnsFullStruct(t *testing.T) {
	fake := &fakeGeoRegionClient{getResp: &geov1.Region{Id: "reg-a", Name: "RU Central"}}
	c := newTestGeoRegionClient(fake)

	first, err := c.Get(context.Background(), "reg-a")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := c.Get(context.Background(), "reg-a")
	if err != nil {
		t.Fatalf("cached Get: %v", err)
	}
	if fake.getCalls != 1 {
		t.Fatalf("expected cache hit on 2nd Get (1 upstream call), got %d", fake.getCalls)
	}
	if second.ID != first.ID || second.Name != first.Name {
		t.Fatalf("cache-hit struct != cache-miss struct: got %+v, want %+v", second, first)
	}
	if second.Name == "" {
		t.Fatalf("cache-hit returned partial struct (Name empty): %+v", second)
	}
}

// TestGeoRegionClient_Get_PerCallDeadlineOnHungPeer — regression под audit-находку
// (concurrency): GeoRegionClient.Get обязан нести собственный per-call дедлайн.
// На unbounded ctx (worker-ctx после baggage.Extract) hung geo не должен вешать
// горутину — клиент возвращает DeadlineExceeded ~за timeout.
func TestGeoRegionClient_Get_PerCallDeadlineOnHungPeer(t *testing.T) {
	c := &GeoRegionClient{regions: blockingRegionClient{}, cache: newValueCache[*domain.Region](geoRegionExistsTTL), timeout: 150 * time.Millisecond}

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
