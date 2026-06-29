// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	iamv1 "github.com/PRO-Robotech/kacho-iam/proto/gen/go/kacho/cloud/iam/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
)

// SyncRegistrar — синхронный owner-tuple registrar: реализует
// fgaregister.Registrar поверх InternalIAMService.RegisterResource. Create-flow
// после durable commit ресурса синхронно регистрирует те же Item'ы, что эмитятся
// в outbox-intent, — owner-grant доступен сразу, без гонки с async drainer'ом.
//
// Каждый Item → один RegisterResource (idempotent: повтор того же tuple → OK).
// Любая ошибка пробрасывается наверх → create-Operation fail-closed; durable
// outbox-intent + register-drainer остаются at-least-once backstop'ом (та же
// идемпотентная регистрация повторно безопасна). Использует тот же mTLS-conn к
// kacho-iam :9091, что и drainer (identity — из client-cert).
type SyncRegistrar struct {
	cli     IAMRegisterRPC
	timeout time.Duration
}

// NewSyncRegistrar собирает registrar поверх IAMRegisterRPC (InternalIAMServiceClient
// или его узкое подмножество). timeout по умолчанию 5s на один RegisterResource —
// create-worker идет на background-ctx без дедлайна, поэтому ограничиваем здесь.
func NewSyncRegistrar(cli IAMRegisterRPC) *SyncRegistrar {
	return &SyncRegistrar{cli: cli, timeout: 5 * time.Second}
}

// Register синхронно регистрирует owner-tuple для каждого Item. Stamp'ит
// source_version текущим временем (монотонно >= source_version intent'а, который
// стампится `now()` внутри writer-TX до commit) — last-source-state-wins. Первая
// ошибка прекращает регистрацию и возвращается (fail-closed).
func (s *SyncRegistrar) Register(ctx context.Context, items []fgaregister.Item) error {
	sv := timestamppb.New(time.Now())
	for _, it := range items {
		cctx := ctx
		var cancel context.CancelFunc
		if s.timeout > 0 {
			cctx, cancel = context.WithTimeout(ctx, s.timeout)
		}
		_, err := s.cli.RegisterResource(cctx, &iamv1.RegisterResourceRequest{
			SubjectId:       it.Tuple.SubjectID,
			Relation:        it.Tuple.Relation,
			Object:          it.Tuple.Object,
			Labels:          it.Labels,
			ParentProjectId: it.ParentProjectID,
			SourceVersion:   sv,
		})
		if cancel != nil {
			cancel()
		}
		if err != nil {
			return fmt.Errorf("sync register owner-tuple %s: %w", it.Tuple.Object, err)
		}
	}
	return nil
}

// Compile-time check.
var _ fgaregister.Registrar = (*SyncRegistrar)(nil)
