// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/auth"
	"github.com/PRO-Robotech/kacho-corelib/retry"
	geov1 "github.com/PRO-Robotech/kacho-geo/proto/gen/go/kacho/cloud/geo/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// geoZoneExistsTTL — TTL кеша «зона существует». Geography (Region/Zone) — домен
// kacho-geo (leaf platform-topology service); VPC валидирует zone_id вызовом
// geo.v1.ZoneService.Get на request-path (Subnet.Create / AddressPool.Create).
// Зоны меняются крайне редко → положительный результат можно кешировать.
// Отрицательный (NotFound) НЕ кешируется (зону могут создать в любой момент).
// Недоступность geo → gRPC Unavailable пробрасывается как есть (fail-closed на
// мутации; чтение уже сохраненных ресурсов zone_id не перепроверяет — dangling-ref
// переживается на уровне Get).
const geoZoneExistsTTL = 60 * time.Second

// GeoZoneClient реализует repo.ZoneRegistry поверх gRPC к kacho-geo
// (geo.v1.ZoneService — owner Geography). Ребро vpc→geo: VPC валидирует zone_id
// через owner-сервис, без собственного зеркала зон.
type GeoZoneClient struct {
	zones geov1.ZoneServiceClient

	mu    sync.RWMutex
	known map[string]time.Time // zoneID → время до которого «существует» валидно
}

// NewGeoZoneClient создает GeoZoneClient. conn — обычно `clients.Build(...)`
// (см. builder.go); принимается как grpc.ClientConnInterface для совместимости
// с corlib `ClientConn` и `*grpc.ClientConn`.
func NewGeoZoneClient(conn grpc.ClientConnInterface) *GeoZoneClient {
	return &GeoZoneClient{
		zones: geov1.NewZoneServiceClient(conn),
		known: make(map[string]time.Time),
	}
}

// Get возвращает зону по id. Маппинг ошибок cross-domain-валидации:
//   - зона не найдена (geo вернул NotFound) → repo.ErrNotFound (use-case
//     транслирует в InvalidArgument: zone_id ссылается на несуществующую зону);
//   - geo недоступен → gRPC Unavailable пробрасывается как есть (fail-closed на
//     мутации; consumer не смог провалидировать zone).
func (c *GeoZoneClient) Get(ctx context.Context, id string) (*domain.Zone, error) {
	c.mu.RLock()
	exp, ok := c.known[id]
	c.mu.RUnlock()
	if ok && time.Now().Before(exp) {
		return &domain.Zone{ID: id}, nil
	}

	var z *domain.Zone
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		resp, rerr := c.zones.Get(auth.PropagateOutgoing(ctx), &geov1.GetZoneRequest{ZoneId: id})
		if rerr != nil {
			if st, ok := status.FromError(rerr); ok && st.Code() == codes.NotFound {
				return repo.ErrNotFound
			}
			return rerr
		}
		z = &domain.Zone{ID: resp.GetId(), RegionID: resp.GetRegionId(), Name: resp.GetName()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.known[id] = time.Now().Add(geoZoneExistsTTL)
	c.mu.Unlock()
	return z, nil
}

// ListIDs возвращает идентификаторы всех зон (для динамического сообщения
// «must be one of: ...»). Без пагинации наружу — зон в системе единицы;
// при необходимости проходит все страницы.
func (c *GeoZoneClient) ListIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		ids = ids[:0]
		var pageToken string
		for {
			resp, rerr := c.zones.List(auth.PropagateOutgoing(ctx), &geov1.ListZonesRequest{PageSize: 1000, PageToken: pageToken})
			if rerr != nil {
				return rerr
			}
			for _, z := range resp.GetZones() {
				ids = append(ids, z.GetId())
			}
			pageToken = resp.GetNextPageToken()
			if pageToken == "" {
				return nil
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}
