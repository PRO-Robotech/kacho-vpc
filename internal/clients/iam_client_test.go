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

	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// blockingProjectClient — iamv1.ProjectServiceClient, чей Get блокируется до отмены
// ctx (alive-but-unresponsive iam). Остальные методы интерфейса не вызываются
// (Exists ходит только в Get) — embed nil-интерфейса даёт их «заглушки».
type blockingProjectClient struct{ iamv1.ProjectServiceClient }

func (blockingProjectClient) Get(ctx context.Context, _ *iamv1.GetProjectRequest, _ ...grpc.CallOption) (*iamv1.Project, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// TestProjectClient_Exists_PerCallDeadlineOnHungPeer — regression под audit-находку
// (concurrency): ProjectClient.Exists обязан нести собственный per-call дедлайн.
// Вызов из Network.Create-worker'а идёт на unbounded ctx; hung iam не должен
// вешать горутину — клиент ограничивает вызов timeout'ом и возвращает
// DeadlineExceeded ~за timeout, не навсегда.
func TestProjectClient_Exists_PerCallDeadlineOnHungPeer(t *testing.T) {
	c := &ProjectClient{cli: blockingProjectClient{}, timeout: 150 * time.Millisecond}

	done := make(chan error, 1)
	go func() {
		_, err := c.Exists(context.Background(), "prj-a")
		done <- err
	}()

	select {
	case err := <-done:
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("expected DeadlineExceeded from hung iam, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Exists did not return: per-call deadline not applied (goroutine hung on unbounded ctx)")
	}
}
