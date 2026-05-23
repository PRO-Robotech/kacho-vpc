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
	computev1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/compute/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// zoneExistsTTL — TTL кеша «зона существует». Geography (Region/Zone) — домен
// kacho-compute (эпик KAC-15): VPC валидирует zone_id вызовом
// compute.v1.ZoneService.Get на request-path (Subnet.Create / Relocate). Зоны
// меняются крайне редко → положительный результат можно кешировать. Отрицательный
// (NotFound) НЕ кешируется (зону могут создать в любой момент). Недоступность
// compute → ошибка (fail-closed на мутации; чтение уже сохранённых ресурсов
// zone_id не перепроверяет — dangling-ref переживается на уровне Get).
const zoneExistsTTL = 60 * time.Second

// ComputeGeographyClient реализует service.ZoneRegistry поверх gRPC к kacho-compute
// (ZoneService — owner Geography). См. workspace CLAUDE.md §«Кросс-доменные ссылки
// на ресурсы».
type ComputeGeographyClient struct {
	zones computev1.ZoneServiceClient

	mu    sync.RWMutex
	known map[string]time.Time // zoneID → время до которого «существует» валидно
}

// NewComputeGeographyClient создаёт ComputeGeographyClient. conn — обычно
// `clients.Build(...)` (см. builder.go); принимается как
// grpc.ClientConnInterface для совместимости с corlib `ClientConn` (KAC-97) и
// `*grpc.ClientConn`.
func NewComputeGeographyClient(conn grpc.ClientConnInterface) *ComputeGeographyClient {
	return &ComputeGeographyClient{
		zones: computev1.NewZoneServiceClient(conn),
		known: make(map[string]time.Time),
	}
}

// Get возвращает зону по id (repo.ErrNotFound для несуществующей; gRPC-ошибку,
// напр. Unavailable, если compute недоступен — пробрасывается как есть).
func (c *ComputeGeographyClient) Get(ctx context.Context, id string) (*domain.Zone, error) {
	c.mu.RLock()
	exp, ok := c.known[id]
	c.mu.RUnlock()
	if ok && time.Now().Before(exp) {
		return &domain.Zone{ID: id}, nil
	}

	var z *domain.Zone
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		resp, rerr := c.zones.Get(auth.PropagateOutgoing(ctx), &computev1.GetZoneRequest{ZoneId: id})
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
	c.known[id] = time.Now().Add(zoneExistsTTL)
	c.mu.Unlock()
	return z, nil
}

// ListIDs возвращает идентификаторы всех зон (для динамического сообщения
// «must be one of: ...»). Без пагинации наружу — зон в системе единицы;
// при необходимости проходит все страницы.
func (c *ComputeGeographyClient) ListIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := retry.OnUnavailable(ctx, func(ctx context.Context) error {
		ids = ids[:0]
		var pageToken string
		for {
			resp, rerr := c.zones.List(auth.PropagateOutgoing(ctx), &computev1.ListZonesRequest{PageSize: 1000, PageToken: pageToken})
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
