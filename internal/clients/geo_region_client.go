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
	geov1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/geo/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// geoRegionExistsTTL — TTL кеша «регион существует». Регионы меняются крайне
// редко → положительный результат кешируется; отрицательный (NotFound) НЕ
// кешируется (регион могут создать в любой момент). Недоступность geo →
// gRPC Unavailable пробрасывается как есть (fail-closed на мутации).
const geoRegionExistsTTL = 60 * time.Second

// GeoRegionClient реализует repo.RegionRegistry поверх gRPC к kacho-geo
// (geo.v1.RegionService — owner Geography). Ребро vpc→geo: VPC валидирует
// region_id REGIONAL-подсети через owner-сервис, без собственного зеркала регионов.
type GeoRegionClient struct {
	regions geov1.RegionServiceClient

	mu    sync.RWMutex
	known map[string]time.Time // regionID → время до которого «существует» валидно
}

// NewGeoRegionClient создает GeoRegionClient поверх общего geo-conn.
func NewGeoRegionClient(conn grpc.ClientConnInterface) *GeoRegionClient {
	return &GeoRegionClient{
		regions: geov1.NewRegionServiceClient(conn),
		known:   make(map[string]time.Time),
	}
}

// Get возвращает регион по id. Маппинг ошибок cross-domain-валидации:
//   - регион не найден (geo вернул NotFound) → repo.ErrNotFound (use-case
//     транслирует в InvalidArgument: region_id ссылается на несуществующий регион);
//   - geo недоступен → gRPC Unavailable пробрасывается как есть (fail-closed на
//     мутации; consumer не смог провалидировать region).
func (c *GeoRegionClient) Get(ctx context.Context, id string) (*domain.Region, error) {
	c.mu.RLock()
	exp, ok := c.known[id]
	c.mu.RUnlock()
	if ok && time.Now().Before(exp) {
		return &domain.Region{ID: id}, nil
	}

	var r *domain.Region
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		resp, rerr := c.regions.Get(auth.PropagateOutgoing(ctx), &geov1.GetRegionRequest{RegionId: id})
		if rerr != nil {
			if st, ok := status.FromError(rerr); ok && st.Code() == codes.NotFound {
				return repo.ErrNotFound
			}
			return rerr
		}
		r = &domain.Region{ID: resp.GetId(), Name: resp.GetName()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.known[id] = time.Now().Add(geoRegionExistsTTL)
	c.mu.Unlock()
	return r, nil
}
