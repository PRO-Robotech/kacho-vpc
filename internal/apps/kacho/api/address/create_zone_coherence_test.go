// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

// RED-фаза (строгий TDD, ban #12) под APPROVED acceptance-док
// docs/specs/sub-phase-nlb-vpc-zone-coherence-acceptance.md — трек B, GAP-3:
// external Address.zone_id — geo-валидация существования.
//
// Инвариант (data-integrity.md §Placement-coherence): «Существование zone_id —
// валидировать peer-вызовом geo.v1.ZoneService.Get, fail-closed. Пропуск (напр.
// непроверенная зона внешнего адреса) — баг». Зеркалит subnet.validateZoneID
// (internal/apps/kacho/api/subnet/helpers.go:185-200,
// InvalidArgument "unknown zone id '%s'"). Отличие: для external Address пустой
// zone_id ВАЛИДЕН (anycast из global-пула) — проверка условная.
//
// RED-состояние (до фикса): CreateAddressUseCase не валидирует
// ExternalSpec.ZoneID / ExternalIpv6Spec.ZoneID через geo — external Address с
// несуществующей зоной создаётся. Execute возвращает (op, nil) вместо sync
// InvalidArgument → ZC-VPC-ADDR-ZONE-01/02 падают по feature-absent.
//
// GREEN-step (rpc-implementer, тот же PR, план Stage 2):
//   1. добавить локальный порт address.ZoneRegistry { Get(ctx, id) (*domain.Zone, error) }
//      (как subnet/iface.go) + wiring в cmd/ (impl clients.GeoZoneClient);
//   2. в Execute — условная geo-validation ExternalSpec.ZoneID/ExternalIpv6Spec.ZoneID
//      (непустой → Get → ErrNotFound → InvalidArgument "unknown zone id '<X>'";
//      пустой → skip), fail-closed Unavailable;
//   3. заменить ТЕЛО хелпера newCreateUCWithZones ниже на инъекцию zr в use-case —
//      единственная точка правки, после чего ZC-VPC-ADDR-ZONE-01/02 → GREEN.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// fakeZoneRegistry — двойник geo ZoneRegistry (geo.v1.ZoneService.Get) для
// existence-валидации external Address.zone_id. Известные зоны → *domain.Zone;
// прочее → repo.ErrNotFound (use-case транслирует в InvalidArgument
// "unknown zone id '<X>'", зеркало subnet.validateZoneID).
type fakeZoneRegistry struct {
	known map[string]bool
}

func newFakeZoneRegistry(known ...string) *fakeZoneRegistry {
	m := make(map[string]bool, len(known))
	for _, z := range known {
		m[z] = true
	}
	return &fakeZoneRegistry{known: m}
}

func (f *fakeZoneRegistry) Get(_ context.Context, id string) (*domain.Zone, error) {
	if f.known[id] {
		return &domain.Zone{ID: id, RegionID: "region-1"}, nil
	}
	return nil, repo.ErrNotFound
}

// newCreateUCWithZones — конструктор CreateAddressUseCase с fake ZoneRegistry.
//
// GREEN-step: заменить тело на инъекцию zr в use-case после добавления
// ZoneRegistry-порта (см. шапку файла). Пока порта нет — zr игнорируется, external
// zone НЕ валидируется → ZC-VPC-ADDR-ZONE-01/02 остаются RED (feature-absent), а
// ZC-VPC-ADDR-ZONE-03/04 проходят (сейчас нет ни валидации, ни over-rejection).
func newCreateUCWithZones(
	kr *kachomock.Repository, sr *repomock.SubnetRepo,
	fc *repomock.ProjectClient, or *repomock.OpsRepo, zr *fakeZoneRegistry,
) *CreateAddressUseCase {
	return NewCreateAddressUseCase(kr, sr, fc, or, nil).WithZoneRegistry(zr)
}

// ---- GAP-3 RED — external zone existence ------------------------------------

// TestCreateUseCase_ZCVPCADDRZONE01_ExternalV4UnknownZone_Rejected — ZC-VPC-ADDR-ZONE-01:
// external IPv4 Address с несуществующей zone_id → sync INVALID_ARGUMENT
// "unknown zone id 'zzz-nonexistent-9'". RED: external zone не валидируется в geo.
func TestCreateUseCase_ZCVPCADDRZONE01_ExternalV4UnknownZone_Rejected(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	zr := newFakeZoneRegistry() // "zzz-nonexistent-9" НЕ известна geo
	uc := newCreateUCWithZones(kr, sr, &repomock.ProjectClient{OK: true}, or, zr)

	_, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:    "f1",
		ExternalSpec: &ExternalAddrSpec{ZoneID: "zzz-nonexistent-9"},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err),
		"external v4 with nonexistent zone must be rejected synchronously")
	require.Equal(t, "unknown zone id 'zzz-nonexistent-9'", status.Convert(err).Message())

	// Operation не создаётся; строка не появляется.
	listUC := NewListAddressesUseCase(kr, nil)
	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Empty(t, addrs, "no Address row for a rejected create")
}

// TestCreateUseCase_ZCVPCADDRZONE02_ExternalV6UnknownZone_Rejected — ZC-VPC-ADDR-ZONE-02:
// симметрия v4/v6 — external IPv6 spec с несуществующей zone_id тоже валидируется.
// RED: external v6 zone не валидируется.
func TestCreateUseCase_ZCVPCADDRZONE02_ExternalV6UnknownZone_Rejected(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	zr := newFakeZoneRegistry()
	uc := newCreateUCWithZones(kr, sr, &repomock.ProjectClient{OK: true}, or, zr)

	_, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:        "f1",
		ExternalIpv6Spec: &ExternalAddrSpec{ZoneID: "zzz-nonexistent-9"},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err),
		"external v6 with nonexistent zone must be rejected synchronously")
	require.Equal(t, "unknown zone id 'zzz-nonexistent-9'", status.Convert(err).Message())
}

// ---- GAP-3 regression-locks -------------------------------------------------

// TestCreateUseCase_ZCVPCADDRZONE03_ExternalKnownZone_OK — ZC-VPC-ADDR-ZONE-03:
// external Address с СУЩЕСТВУЮЩЕЙ zone_id → проходит existence-check → создаётся.
// Explicit address (без pool-аллокации) делает исход детерминированным.
func TestCreateUseCase_ZCVPCADDRZONE03_ExternalKnownZone_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	zr := newFakeZoneRegistry("region-1-a") // зона существует
	uc := newCreateUCWithZones(kr, sr, &repomock.ProjectClient{OK: true}, or, zr)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:    "f1",
		Name:         "addr-known-zone",
		ExternalSpec: &ExternalAddrSpec{Address: "203.0.113.10", ZoneID: "region-1-a"},
	})
	require.NoError(t, err)
	require.Nil(t, repomock.AwaitOpDone(t, or, op.ID).Error)

	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	require.Equal(t, "region-1-a", addrs[0].ExternalIpv4.ZoneID)
}

// TestCreateUseCase_ZCVPCADDRZONE04_AnycastEmptyZone_OK — ZC-VPC-ADDR-ZONE-04:
// external Address БЕЗ zone_id (anycast / global-пул) → existence-check ПРОПУСКАЕТСЯ
// (в отличие от Subnet, где ZONAL требует непустой zone_id) → создаётся.
// Regression-lock против over-rejection пустой зоны.
func TestCreateUseCase_ZCVPCADDRZONE04_AnycastEmptyZone_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	zr := newFakeZoneRegistry() // пустая зона не должна дёргать lookup
	uc := newCreateUCWithZones(kr, sr, &repomock.ProjectClient{OK: true}, or, zr)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:    "f1",
		Name:         "addr-anycast",
		ExternalSpec: &ExternalAddrSpec{Address: "203.0.113.11"}, // zone_id пуст
	})
	require.NoError(t, err, "empty external zone (anycast) must NOT be rejected")
	require.Nil(t, repomock.AwaitOpDone(t, or, op.ID).Error)

	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	require.Equal(t, "", addrs[0].ExternalIpv4.ZoneID)
}

// TestCreateUseCase_ZCVPCADDRZONE05_InternalInheritsSubnet_OK — ZC-VPC-ADDR-ZONE-05
// (boundary): internal Address зону НЕ несёт (наследует через subnet_id); GAP-3
// добавляет проверку ТОЛЬКО для external-spec'ов — internal-путь не меняется.
func TestCreateUseCase_ZCVPCADDRZONE05_InternalInheritsSubnet_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := &domain.Subnet{
		ID:           ids.NewID(ids.PrefixSubnet),
		ProjectID:    "f1",
		NetworkID:    ids.NewID(ids.PrefixNetwork),
		Name:         domain.RcNameVPC("sn-internal"),
		V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, _ = sr.Insert(context.Background(), sub)
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:    "f1",
		InternalSpec: &InternalAddrSpec{SubnetID: sub.ID},
	})
	require.NoError(t, err, "internal Address must not undergo external-zone geo-validation")
	require.Nil(t, repomock.AwaitOpDone(t, or, op.ID).Error)

	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	require.Equal(t, domain.AddressTypeInternal, addrs[0].Type)
}
