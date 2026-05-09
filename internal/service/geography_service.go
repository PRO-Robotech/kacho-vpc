// Package service — Region/Zone use-cases. Глобальный admin-only ресурс.
package service

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// RegionRepo — port-интерфейс репозитория Region.
type RegionRepo interface {
	Get(ctx context.Context, id string) (*domain.Region, error)
	List(ctx context.Context, p Pagination) ([]*domain.Region, string, error)
	Insert(ctx context.Context, v *domain.Region) (*domain.Region, error)
	Update(ctx context.Context, v *domain.Region) (*domain.Region, error)
	Delete(ctx context.Context, id string) error
}

// ZoneRepo — port-интерфейс репозитория Zone.
type ZoneRepo interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
	List(ctx context.Context, regionID string, p Pagination) ([]*domain.Zone, string, error)
	Insert(ctx context.Context, v *domain.Zone) (*domain.Zone, error)
	Update(ctx context.Context, v *domain.Zone) (*domain.Zone, error)
	Delete(ctx context.Context, id string) error
}

// -- Region service --

type RegionService struct{ repo RegionRepo }

func NewRegionService(r RegionRepo) *RegionService { return &RegionService{repo: r} }

func (s *RegionService) Create(ctx context.Context, id, name string) (*domain.Region, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	v := &domain.Region{ID: id, Name: name, CreatedAt: time.Now().UTC()}
	return s.repo.Insert(ctx, v)
}

func (s *RegionService) Get(ctx context.Context, id string) (*domain.Region, error) {
	return s.repo.Get(ctx, id)
}

func (s *RegionService) List(ctx context.Context, p Pagination) ([]*domain.Region, string, error) {
	return s.repo.List(ctx, p)
}

func (s *RegionService) Update(ctx context.Context, id, name string) (*domain.Region, error) {
	cur, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cur.Name = name
	return s.repo.Update(ctx, cur)
}

func (s *RegionService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// -- Zone service --

type ZoneService struct {
	zones   ZoneRepo
	regions RegionRepo
}

func NewZoneService(z ZoneRepo, r RegionRepo) *ZoneService {
	return &ZoneService{zones: z, regions: r}
}

func (s *ZoneService) Create(ctx context.Context, id, regionID, name string) (*domain.Zone, error) {
	id = strings.TrimSpace(id)
	regionID = strings.TrimSpace(regionID)
	if id == "" || regionID == "" {
		return nil, status.Error(codes.InvalidArgument, "id and region_id required")
	}
	if _, err := s.regions.Get(ctx, regionID); err != nil {
		return nil, status.Errorf(codes.NotFound, "Region %s not found", regionID)
	}
	v := &domain.Zone{ID: id, RegionID: regionID, Name: name, CreatedAt: time.Now().UTC()}
	return s.zones.Insert(ctx, v)
}

func (s *ZoneService) Get(ctx context.Context, id string) (*domain.Zone, error) {
	return s.zones.Get(ctx, id)
}

func (s *ZoneService) List(ctx context.Context, regionID string, p Pagination) ([]*domain.Zone, string, error) {
	return s.zones.List(ctx, regionID, p)
}

func (s *ZoneService) Update(ctx context.Context, id, name string) (*domain.Zone, error) {
	cur, err := s.zones.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cur.Name = name
	return s.zones.Update(ctx, cur)
}

func (s *ZoneService) Delete(ctx context.Context, id string) error {
	return s.zones.Delete(ctx, id)
}
