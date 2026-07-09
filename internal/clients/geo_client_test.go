// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	geov1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/geo/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// fakeGeoZoneClient — детерминированный stub geov1.ZoneServiceClient под unit-
// тесты GeoZoneClient. Программируется ответами Get/List. -race-safe не нужен:
// каждый тест использует свой инстанс последовательно.
type fakeGeoZoneClient struct {
	getResp  *geov1.Zone
	getErr   error
	getCalls int

	// getErrSeq — последовательность ошибок per Get-вызов (nil = успех с getResp).
	// Позволяет смоделировать «Unavailable на 1-й попытке, успех на retry».
	getErrSeq []error
}

func (f *fakeGeoZoneClient) Get(_ context.Context, in *geov1.GetZoneRequest, _ ...grpc.CallOption) (*geov1.Zone, error) {
	idx := f.getCalls
	f.getCalls++
	if idx < len(f.getErrSeq) {
		if e := f.getErrSeq[idx]; e != nil {
			return nil, e
		}
		return f.getResp, nil
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getResp, nil
}

// List удовлетворяет geov1.ZoneServiceClient (Get+List). ListIDs удалён из
// ZoneRegistry-порта (vestigial, LEAN), поэтому List программировать не нужно.
func (f *fakeGeoZoneClient) List(_ context.Context, _ *geov1.ListZonesRequest, _ ...grpc.CallOption) (*geov1.ListZonesResponse, error) {
	return &geov1.ListZonesResponse{}, nil
}

// newTestGeoZoneClient собирает GeoZoneClient поверх fake ZoneServiceClient,
// инъектируя stub в обход gRPC-conn (unit-уровень, без сети).
func newTestGeoZoneClient(fake geov1.ZoneServiceClient) *GeoZoneClient {
	return &GeoZoneClient{zones: fake, cache: newExistsCache(geoZoneExistsTTL), timeout: defaultPeerCallTimeout}
}

// blockingZoneClient — geov1.ZoneServiceClient, чей Get блокируется до отмены ctx.
// Моделирует alive-but-unresponsive geo (deadlocked handler / GC-pause): gRPC
// keepalive не срабатывает, пока stream активен. Без per-call дедлайна вызов
// висит навсегда на unbounded worker-ctx.
type blockingZoneClient struct{ geov1.ZoneServiceClient }

func (blockingZoneClient) Get(ctx context.Context, _ *geov1.GetZoneRequest, _ ...grpc.CallOption) (*geov1.Zone, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// TestGeoZoneClient_Get_PerCallDeadlineOnHungPeer — regression под audit-находку
// (concurrency): peer-клиент обязан нести собственный per-call дедлайн. Вызов идёт
// на unbounded ctx (context.Background — как worker-ctx после baggage.Extract);
// hung geo не должен вешать горутину — клиент сам ограничивает вызов timeout'ом и
// возвращает DeadlineExceeded ~за timeout, не навсегда.
func TestGeoZoneClient_Get_PerCallDeadlineOnHungPeer(t *testing.T) {
	c := &GeoZoneClient{zones: blockingZoneClient{}, cache: newExistsCache(geoZoneExistsTTL), timeout: 150 * time.Millisecond}

	done := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "zone-a")
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

func TestGeoZoneClient_Get_FoundOK(t *testing.T) {
	fake := &fakeGeoZoneClient{getResp: &geov1.Zone{Id: "zone-a", RegionId: "zone", Name: "RU Central A"}}
	c := newTestGeoZoneClient(fake)

	z, err := c.Get(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("expected ok, got err: %v", err)
	}
	if z.ID != "zone-a" || z.RegionID != "zone" || z.Name != "RU Central A" {
		t.Fatalf("unexpected zone: %+v", z)
	}
}

func TestGeoZoneClient_Get_NotFoundMapsToErrNotFound(t *testing.T) {
	fake := &fakeGeoZoneClient{getErr: status.Error(codes.NotFound, "Zone no-such-zone not found")}
	c := newTestGeoZoneClient(fake)

	_, err := c.Get(context.Background(), "no-such-zone")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("expected repo.ErrNotFound (use-case maps to InvalidArgument), got: %v", err)
	}
}

func TestGeoZoneClient_Get_GeoDownFailsClosed(t *testing.T) {
	// geo постоянно недоступен (Unavailable). retry.OnUnavailable исчерпает
	// бэк-офф (или ctx-deadline) и вернет error — НЕ молчаливый успех. Это и есть
	// fail-closed для мутаций: consumer не смог
	// провалидировать zone → Create обязан упасть. Bounding ctx коротким дедлайном
	// держит unit-тест быстрым (retry прекращается по ctx.Done).
	fake := &fakeGeoZoneClient{getErr: status.Error(codes.Unavailable, "geo unreachable")}
	c := newTestGeoZoneClient(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Get(ctx, "zone-a")
	if err == nil {
		t.Fatal("expected error when geo is down (fail-closed), got nil")
	}
}

func TestGeoZoneClient_Get_RetriesUnavailableThenSucceeds(t *testing.T) {
	// Unavailable на 1-й попытке (peer rolling-restart), успех на retry —
	// retry.OnUnavailable должен сделать повтор и вернуть зону.
	fake := &fakeGeoZoneClient{
		getErrSeq: []error{status.Error(codes.Unavailable, "geo restarting"), nil},
		getResp:   &geov1.Zone{Id: "zone-a", RegionId: "zone"},
	}
	c := newTestGeoZoneClient(fake)

	z, err := c.Get(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if z.ID != "zone-a" {
		t.Fatalf("unexpected zone: %+v", z)
	}
	if fake.getCalls != 2 {
		t.Fatalf("expected 2 calls (1 Unavailable + 1 retry success), got %d", fake.getCalls)
	}
}

func TestGeoZoneClient_Get_PositiveCacheSkipsSecondCall(t *testing.T) {
	fake := &fakeGeoZoneClient{getResp: &geov1.Zone{Id: "zone-a", RegionId: "zone"}}
	c := newTestGeoZoneClient(fake)

	if _, err := c.Get(context.Background(), "zone-a"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if _, err := c.Get(context.Background(), "zone-a"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if fake.getCalls != 1 {
		t.Fatalf("expected 1 upstream call (positive cache hit on 2nd), got %d", fake.getCalls)
	}
}
