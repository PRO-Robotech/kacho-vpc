// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package clients — register-drainer FGA transactional-outbox.
//
// Декодирует одну строку fga_register_outbox в tuple и применяет его к FGA ЧЕРЕЗ
// kacho-iam — модули в FGA напрямую не ходят. Apply — это один sync unary-вызов
// InternalIAMService.RegisterResource (event_type fga.register) либо
// UnregisterResource (fga.unregister); оба — Internal-only :9091 RPC, идемпотентные
// по контракту (повтор того же tuple → OK, НЕ AlreadyExists).
//
// Классификация ошибок (ее потребляет corelib outbox/drainer):
//   - OK                    → nil (drainer ставит sent_at).
//   - codes.InvalidArgument → ErrPermanent (malformed tuple = poison, без бесконечных
//     ретраев).
//   - codes.PermissionDenied → transient (raw): отсутствующий grant
//     fga_writer@iam_fgaproxy:system — это вопрос порядка provisioning'а, который
//     лечится после применения SA-grant-миграции; ретрай дает owner-tuple осесть,
//     поэтому НЕ должен poison'ить (как в compute/nlb и в corelib-классификаторе).
//   - все остальное (Unavailable, DeadlineExceeded, транспорт)
//     → transient (raw) → drainer ретраит с backoff; intent остается durable
//     (sent_at NULL) и НЕ теряется.
//
// Clean Architecture: этот адаптер (clients/) реализует port drainer'а Applier;
// импортирует grpc-stubs (iamv1) + чистый domain-тип tuple (fgaregister) — без pgx,
// без use-case/transport-слоя.
package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-corelib/outbox/drainer"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
)

// IAMRegisterRPC — узкое подмножество iamv1.InternalIAMServiceClient, нужное
// register-applier'у: только два FGA-proxy RPC. Полный InternalIAMServiceClient
// его удовлетворяет; test-fake реализует только эти два метода.
type IAMRegisterRPC interface {
	RegisterResource(ctx context.Context, in *iamv1.RegisterResourceRequest, opts ...grpc.CallOption) (*iamv1.RegisterResourceResponse, error)
	UnregisterResource(ctx context.Context, in *iamv1.UnregisterResourceRequest, opts ...grpc.CallOption) (*iamv1.UnregisterResourceResponse, error)
}

// iamRegisterRPC — неэкспортируемый alias для эргономики in-package тестов.
type iamRegisterRPC = IAMRegisterRPC

// FGARegisterPayload — payload-тип T для drainer'а. Это alias на чистый domain-тип
// fgaregister.Payload, чтобы repo-writer (который его эмитит) и drainer-applier
// (который его применяет) делили ровно одну форму. Несет tuple + mirror-feed
// (labels + parent_project_id + source_version).
type FGARegisterPayload = fgaregister.Payload

// DecodeFGARegisterPayload — Decoder[FGARegisterPayload] для corelib-drainer'а:
// разбирает payload одной строки fga_register_outbox в Payload (tuple + mirror-feed).
// Строка с голым Tuple декодируется с пустым mirror-feed. Malformed или неполный
// (пустые subject/relation/object) payload — баг на стороне вызывающего, ретрай его
// не починит → ErrPermanent (drainer poison'ит строку, а не ретраит вечно).
func DecodeFGARegisterPayload(payload []byte) (FGARegisterPayload, error) {
	p, err := fgaregister.Decode(payload)
	if err != nil {
		return p, errors.Join(drainer.ErrPermanent, fmt.Errorf("decode fga register payload: %w", err))
	}
	if !p.Valid() {
		return p, errors.Join(drainer.ErrPermanent,
			fmt.Errorf("incomplete fga register payload: subject_id/relation/object required"))
	}
	return p, nil
}

// NewIAMRegisterApplier строит corelib-drainer Applier[FGARegisterPayload] поверх
// клиента IAMRegisterRPC. eventType выбирает register или unregister. Register
// прокидывает mirror-feed (labels + parent_project_id + source_version), чтобы
// kacho-iam материализовал resource_mirror для селектора; unregister прокидывает
// только идентичность tuple (+ source_version-tombstone).
func NewIAMRegisterApplier(c IAMRegisterRPC) drainer.Applier[FGARegisterPayload] {
	return func(ctx context.Context, eventType string, p FGARegisterPayload) error {
		switch eventType {
		case fgaregister.EventRegister:
			_, err := c.RegisterResource(ctx, &iamv1.RegisterResourceRequest{
				SubjectId:       p.SubjectID,
				Relation:        p.Relation,
				Object:          p.Object,
				Labels:          p.Labels,
				ParentProjectId: p.ParentProjectID,
				SourceVersion:   sourceVersionPB(p.SourceVersion),
			})
			return classifyRegisterErr(err)
		case fgaregister.EventUnregister:
			_, err := c.UnregisterResource(ctx, &iamv1.UnregisterResourceRequest{
				SubjectId: p.SubjectID,
				Relation:  p.Relation,
				Object:    p.Object,
			})
			return classifyRegisterErr(err)
		default:
			// Неизвестный event_type — баг вызывающего; применить нечего, poison.
			return errors.Join(drainer.ErrPermanent,
				fmt.Errorf("unknown fga register event_type %q", eventType))
		}
	}
}

// sourceVersionPB конвертирует монотонный source_version в proto-timestamp.
// Zero (строка без stamp'а) → nil; IAM трактует nil как -infinity — он никогда
// не выигрывает монотонное сравнение.
func sourceVersionPB(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// classifyRegisterErr маппит ошибку IAM RPC на transient/permanent-контракт
// drainer'а. nil → nil (успех). InvalidArgument → permanent (poison: malformed tuple
// ретраем не починить). Все остальное — включая PermissionDenied (grant fga_writer
// еще не засеян), Unavailable и транспорт — → transient (ретрай, intent остается
// durable: fail-closed, но не теряется). Poison'ит только InvalidArgument.
func classifyRegisterErr(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.InvalidArgument {
		return errors.Join(drainer.ErrPermanent, fmt.Errorf("iam register apply: %w", err))
	}
	// Unavailable / DeadlineExceeded / PermissionDenied / Internal / транспорт → transient.
	return fmt.Errorf("iam register apply: %w", err)
}
