// Package handler — internal_address_handler.go реализует
// kacho.cloud.vpc.v1.InternalAddressService — internal RPC, по которому
// kacho-vpc-controllers устанавливает allocated internal IP-адрес после
// успешного запроса в NetBox available-ips API.
//
// Handler НЕ выставлен через api-gateway — он живёт на cluster-internal порту
// (KACHO_VPC_INTERNAL_PORT) рядом с InternalWatchService.
//
// Контракт:
//   - SetInternalIP(address_id, ip): UPDATE addresses SET internal_ipv4.address = ip
//     если address существует и его internal_ipv4 не nil. Атомарно эмитит
//     outbox-event "Address.UPDATED".
//   - Идемпотентен: если internal_ipv4.address уже равен заданному ip — noop
//     без emit-а.
//   - Несовпадение (попытка перезаписать уже allocated отличный IP) →
//     FailedPrecondition. Защищает от race в случае двойного allocate.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/outbox"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// InternalAddressHandler реализует vpcv1.InternalAddressServiceServer.
type InternalAddressHandler struct {
	vpcv1.UnimplementedInternalAddressServiceServer
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewInternalAddressHandler создаёт handler.
func NewInternalAddressHandler(pool *pgxpool.Pool, log *slog.Logger) *InternalAddressHandler {
	return &InternalAddressHandler{pool: pool, log: log}
}

// SetInternalIP реализует RPC.
//
// Пошагово:
//  1. Validate input (address_id non-empty, ip is valid IPv4).
//  2. Begin tx.
//  3. SELECT addresses.internal_ipv4 FOR UPDATE.
//  4. Если row не существует → NotFound. Если internal_ipv4 == nil → FailedPrecondition.
//  5. Если internal_ipv4.address уже == req.ip → noop, return success.
//  6. Если internal_ipv4.address не пуст и != req.ip → FailedPrecondition.
//  7. UPDATE addresses SET internal_ipv4 = jsonb_set(...,$ip) + emit outbox.
//  8. Commit.
func (h *InternalAddressHandler) SetInternalIP(ctx context.Context, req *vpcv1.SetInternalIPRequest) (*vpcv1.SetInternalIPResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if req.GetIp() == "" {
		return nil, status.Error(codes.InvalidArgument, "ip required")
	}
	if parsed := net.ParseIP(req.GetIp()); parsed == nil || parsed.To4() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "ip must be IPv4 dotted-quad, got %q", req.GetIp())
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, internalMapErr("begin tx", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the row to avoid races between two controller-replicas reconciling
	// the same Address concurrently.
	var (
		intRaw   []byte
		folderID string
		addrID   string
		name     string
		desc     string
	)
	err = tx.QueryRow(ctx, `
		SELECT id, folder_id, name, description, internal_ipv4
		FROM addresses
		WHERE id = $1
		FOR UPDATE
	`, req.AddressId).Scan(&addrID, &folderID, &name, &desc, &intRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Errorf(codes.NotFound, "Address %s not found", req.AddressId)
		}
		return nil, internalMapErr("read address", err)
	}
	if intRaw == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"Address %s has no internal_ipv4 spec; SetInternalIP applies only to internal addresses",
			req.AddressId)
	}

	var current map[string]any
	if err := json.Unmarshal(intRaw, &current); err != nil {
		return nil, internalMapErr("decode address spec", err)
	}
	currentIP, _ := current["address"].(string)
	if currentIP == req.Ip {
		// Idempotent: уже выставлен этот же IP.
		return &vpcv1.SetInternalIPResponse{}, nil
	}
	if currentIP != "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"Address %s already has internal_ipv4.address=%q; refusing to overwrite with %q",
			req.AddressId, currentIP, req.Ip)
	}

	// UPDATE + RETURNING чтобы получить полный snapshot для outbox payload.
	tag, err := tx.Exec(ctx, `
		UPDATE addresses
		SET internal_ipv4 = jsonb_set(internal_ipv4, '{address}', to_jsonb($2::text), true)
		WHERE id = $1
		  AND internal_ipv4 IS NOT NULL
		  AND COALESCE(internal_ipv4->>'address', '') = ''
	`, req.AddressId, req.Ip)
	if err != nil {
		return nil, internalMapErr("update address", err)
	}
	if tag.RowsAffected() == 0 {
		// Кто-то нас обогнал между SELECT FOR UPDATE и UPDATE (теоретически
		// невозможно при FOR UPDATE — но defensive). Возвращаем Aborted.
		return nil, status.Errorf(codes.Aborted, "concurrent update: re-read and retry")
	}

	// Emit outbox-event Address.UPDATED. Минимальный payload: id + new IP.
	// Полный snapshot не нужен — consumer (controllers) использует payload
	// только для трассировки.
	payload := map[string]any{
		"id":            addrID,
		"folder_id":     folderID,
		"name":          name,
		"description":   desc,
		"internal_ipv4": map[string]any{"address": req.Ip, "subnet_id": current["subnet_id"]},
	}
	if err := outbox.Emit(ctx, tx, "vpc_outbox", "Address", addrID, "UPDATED", payload); err != nil {
		return nil, internalMapErr("emit outbox", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, internalMapErr("commit tx", err)
	}
	h.log.Info("internal ip set", "address_id", addrID, "ip", req.Ip)
	return &vpcv1.SetInternalIPResponse{}, nil
}
