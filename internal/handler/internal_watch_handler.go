// Package handler содержит gRPC-handler-ы (тонкий transport-слой).
//
// internal_watch_handler.go реализует kacho.cloud.vpc.v1.InternalWatchService —
// internal RPC, по которому kacho-vpc-controllers подписывается на стрим
// событий из vpc_outbox (Outbox pattern + LISTEN/NOTIFY wake-up).
//
// Handler НЕ выставлен через api-gateway — он слушает на отдельном
// cluster-internal порту (KACHO_VPC_INTERNAL_PORT).
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// catchupBatchSize — сколько событий читаем за один SELECT при initial-catchup.
const catchupBatchSize = 100

// InternalWatchHandler реализует vpcv1.InternalWatchServiceServer.
type InternalWatchHandler struct {
	vpcv1.UnimplementedInternalWatchServiceServer
	pool       *pgxpool.Pool
	dsn        string
	log        *slog.Logger
	streamSlot chan struct{} // semaphore: cap = max concurrent streams
}

// NewInternalWatchHandler создаёт handler.
//
// pool — pgxpool DB kacho_vpc, используется для catchup-SELECT'ов.
// dsn — отдельный connection string для dedicated LISTEN-соединения
// (открывается per-stream через pgx.Connect, не уходит в pool).
// maxStreams — лимит одновременных Watch-streams (защита от Postgres
// connection-pool exhaustion одним buggy/looping клиентом). Если 0 —
// fallback 32 (см. config.WatchMaxStreams).
func NewInternalWatchHandler(pool *pgxpool.Pool, dsn string, log *slog.Logger, maxStreams int) *InternalWatchHandler {
	if maxStreams <= 0 {
		maxStreams = 32
	}
	return &InternalWatchHandler{
		pool:       pool,
		dsn:        dsn,
		log:        log,
		streamSlot: make(chan struct{}, maxStreams),
	}
}

// Watch реализует server-stream подписки.
//
// Алгоритм:
//  1. Acquire dedicated pgx-connection (LISTEN вне pool, не возвращается);
//  2. LISTEN vpc_outbox — ждём pg_notify от trigger-а;
//  3. Initial catchup: SELECT * FROM vpc_outbox WHERE sequence_no > cursor
//     AND ($kinds=NULL OR resource_kind=ANY($kinds)) ORDER BY sequence_no
//     LIMIT 100. Повторять, пока batch < 100.
//  4. Loop: WaitForNotification(ctx) с deadline 30s (на случай missed-notify) →
//     re-read events since cursor → стримим.
//
// Граничные случаи:
//   - client cancel: ctx стрима cancelled → возвращаем nil (graceful).
//   - LISTEN-conn drop: возвращаем Unavailable, client делает retry.
//   - DB transient: возвращаем Internal без leak'а pgx-сообщения в текст.
func (h *InternalWatchHandler) Watch(req *vpcv1.WatchRequest, stream vpcv1.InternalWatchService_WatchServer) error {
	ctx := stream.Context()

	// Acquire stream slot — non-blocking: если все слоты заняты, отвечаем
	// ResourceExhausted сразу, не задерживаем клиента. Защита от DoS:
	// один buggy client (или admin UI с тысячью stale-tabs) не может выпить
	// connection pool под LISTEN.
	select {
	case h.streamSlot <- struct{}{}:
		defer func() { <-h.streamSlot }()
	default:
		return status.Error(codes.ResourceExhausted,
			"too many concurrent watch streams (limit reached)")
	}

	cursor := req.GetFromSequenceNo()
	kinds := req.GetKinds()

	h.log.Info("watch stream started",
		"from_sequence_no", cursor,
		"kinds", kinds)

	// Dedicated pgx.Conn вне пула — гарантированная изоляция LISTEN-сессии.
	// При abnormal exit (panic, server kill) Close() из defer не выполнится, но
	// сам conn закроется TCP'шно — pool затронут не будет (TODO #15 closed).
	//
	// Connect под inner timeout (2s): защита от self-DoS если Postgres
	// перегружен. Слот semaphore удерживается ровно столько; иначе медленный
	// Connect размазал бы 32 слота на 5+ секунд под нагрузкой → клиенты
	// получали бы ResourceExhausted всё это время.
	connectCtx, connectCancel := context.WithTimeout(ctx, 2*time.Second)
	conn, err := pgx.Connect(connectCtx, h.dsn)
	connectCancel()
	if err != nil {
		// Generic Unavailable без leak'а pgx-text (db hostname / port / sslmode).
		return status.Error(codes.Unavailable, "watch backend unavailable")
	}
	defer func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Close(closeCtx)
		cancelClose()
	}()

	// LISTEN на trigger-канал; идентификатор — literal, не из user-input.
	if _, err := conn.Exec(ctx, "LISTEN vpc_outbox"); err != nil {
		return internalMapErr("watch listen failed", err)
	}
	// UNLISTEN не обязателен (conn будет закрыт), но оставлен для symmetry.
	defer func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = conn.Exec(closeCtx, "UNLISTEN vpc_outbox")
		cancelClose()
	}()

	// 1. Initial catchup.
	if newCursor, err := h.streamSince(ctx, conn, cursor, kinds, stream); err != nil {
		return err
	} else {
		cursor = newCursor
	}

	// 2. Loop: wait NOTIFY / timeout / ctx.Done.
	// 30s timeout — periodic re-poll на случай missed NOTIFY (например при
	// listener-pause из-за GC). Bounded resource usage.
	for {
		if err := ctx.Err(); err != nil {
			h.log.Info("watch stream cancelled", "err", err)
			return nil
		}
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := conn.WaitForNotification(waitCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// timeout — re-poll анывай (на случай missed event); ctx canceled
				// — выходим в начало loop, на ctx.Err() check.
				if ctx.Err() != nil {
					return nil
				}
				// timeout: продолжаем в re-poll.
			} else {
				// pg_notify connection drop / другое.
				// Без leak'а pgx-detail — клиент видит generic Unavailable.
				return status.Error(codes.Unavailable, "watch notification stream lost")
			}
		}

		// 3. Re-read since cursor (несколько NOTIFY могут coalesce — читаем
		// все пропущенные events).
		if newCursor, err := h.streamSince(ctx, conn, cursor, kinds, stream); err != nil {
			return err
		} else {
			cursor = newCursor
		}
	}
}

// streamSince читает все события из vpc_outbox с sequence_no > cursor (и
// resource_kind ∈ kinds, если задан) и шлёт их в stream. Возвращает новый
// cursor (= sequence_no последнего отправленного события).
//
// Делает несколько SELECT-ов по batchSize до тех пор, пока не вычерпаем все
// события (batch < batchSize ⇒ end-of-data).
func (h *InternalWatchHandler) streamSince(
	ctx context.Context,
	conn *pgx.Conn,
	cursor int64,
	kinds []string,
	stream vpcv1.InternalWatchService_WatchServer,
) (int64, error) {
	for {
		args := []any{cursor}
		var kindFilter string
		if len(kinds) > 0 {
			kindFilter = " AND resource_kind = ANY($2)"
			args = append(args, kinds)
		}
		q := fmt.Sprintf(`
			SELECT sequence_no, resource_kind, resource_id, event_type, payload, created_at
			FROM vpc_outbox
			WHERE sequence_no > $1%s
			ORDER BY sequence_no ASC
			LIMIT %d
		`, kindFilter, catchupBatchSize)

		rows, err := conn.Query(ctx, q, args...)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return cursor, nil
			}
			return cursor, internalMapErr("query outbox", err)
		}

		count := 0
		for rows.Next() {
			var seq int64
			var kind, id, eventType string
			var payloadJSON []byte
			var createdAt time.Time
			if err := rows.Scan(&seq, &kind, &id, &eventType, &payloadJSON, &createdAt); err != nil {
				rows.Close()
				return cursor, internalMapErr("scan outbox", err)
			}

			payloadStruct, err := jsonBytesToStruct(payloadJSON)
			if err != nil {
				h.log.Warn("watch: bad payload JSON", "sequence_no", seq, "err", err)
				payloadStruct = &structpb.Struct{Fields: map[string]*structpb.Value{}}
			}

			ev := &vpcv1.Event{
				SequenceNo:   seq,
				ResourceKind: kind,
				ResourceId:   id,
				EventType:    eventType,
				Payload:      payloadStruct,
				CreatedAt:    timestamppb.New(createdAt),
			}
			if err := stream.Send(ev); err != nil {
				rows.Close()
				return cursor, err
			}
			cursor = seq
			count++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return cursor, internalMapErr("outbox iter", err)
		}

		if count < catchupBatchSize {
			return cursor, nil
		}
		// если получили full batch — продолжаем читать.
	}
}

// jsonBytesToStruct декодирует raw JSON-bytes (object) в structpb.Struct.
// Использует промежуточный map для устойчивости к произвольным JSON-объектам
// (без top-level не-object — outbox payload всегда object).
func jsonBytesToStruct(raw []byte) (*structpb.Struct, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return &structpb.Struct{Fields: map[string]*structpb.Value{}}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return structpb.NewStruct(m)
}
