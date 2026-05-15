// Package service — AddressPool CRUD + bindings.
//
// Internal-only сервис: используется через kacho.cloud.vpc.v1.InternalAddressPoolService
// gRPC. Не выставляется через api-gateway.
package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressPoolService — use-cases для AddressPool + bindings + label-cascade resolve.
//
// AddressPool — глобальный infrastructure-ресурс (как Region/Zone), не привязан
// к Org/Cloud/Folder. Управляется только админом через InternalAddressPoolService.
type AddressPoolService struct {
	pools        AddressPoolRepo
	bindings     AddressPoolBindingRepo
	cloudSel     CloudPoolSelectorRepo
	addrRepo     AddressRepo
	netRepo      NetworkRepo
	subnetRepo   SubnetRepo
	folderClient FolderClient // для folder_id → cloud_id resolve в cascade
	zoneReg      ZoneRegistry // существование zone_id (Geography — домен kacho-compute, эпик KAC-15); nil → проверка пропускается
}

func NewAddressPoolService(
	p AddressPoolRepo,
	b AddressPoolBindingRepo,
	cloudSel CloudPoolSelectorRepo,
	addr AddressRepo,
	net NetworkRepo,
	sub SubnetRepo,
	folderClient FolderClient,
	zoneReg ZoneRegistry,
) *AddressPoolService {
	return &AddressPoolService{
		pools: p, bindings: b, cloudSel: cloudSel,
		addrRepo: addr, netRepo: net, subnetRepo: sub,
		folderClient: folderClient, zoneReg: zoneReg,
	}
}

// CreatePoolReq — параметры создания пула.
//
// KAC-71: cidr_blocks split на v4_cidr_blocks + v6_cidr_blocks. Хотя бы одно
// поле должно быть непустым (валидация B5; миграция 0022 имеет defensive guard
// на pre-existing rows). Family каждого блока обязательна (REQ-IPL-CR-05 / B6):
// IPv6-префикс в V4CIDRBlocks → InvalidArgument, и симметрично.
type CreatePoolReq struct {
	Name             string
	Description      string
	Labels           map[string]string
	V4CIDRBlocks     []string
	V6CIDRBlocks     []string
	Kind             domain.AddressPoolKind
	ZoneID           string // ru-central1-a; "" = глобальный пул (default fallback)
	IsDefault        bool
	SelectorLabels   map[string]string
	SelectorPriority int32
}

func (s *AddressPoolService) Create(ctx context.Context, req CreatePoolReq) (*domain.AddressPool, error) {
	if req.Kind == domain.AddressPoolKindUnspecified {
		return nil, status.Error(codes.InvalidArgument, "kind must be specified")
	}
	// REQ-IPL-CR-04 / B5: хотя бы одно из v4_cidr_blocks / v6_cidr_blocks
	// непусто. Pool без CIDR — бессмысленен (нельзя ни v4-, ни v6-аллокацию).
	if len(req.V4CIDRBlocks) == 0 && len(req.V6CIDRBlocks) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"v4_cidr_blocks and v6_cidr_blocks must not be both empty")
	}
	// REQ-IPL-CR-05 / B6: family-strict валидация каждого слота. IPv6-prefix в
	// V4CIDRBlocks или IPv4 в V6CIDRBlocks → InvalidArgument с verbatim текстом
	// (см. acceptance §0 «CIDR family detection в API-слое»).
	if err := validateAddressPoolCIDRs("v4_cidr_blocks", req.V4CIDRBlocks, familyV4Strict); err != nil {
		return nil, err
	}
	if err := validateAddressPoolCIDRs("v6_cidr_blocks", req.V6CIDRBlocks, familyV6Strict); err != nil {
		return nil, err
	}
	// zone_id existence — Geography (Region/Zone) — домен kacho-compute (эпик KAC-15):
	// FK address_pools.zone_id → zones убрана; существование зоны проверяем вызовом
	// compute.v1.ZoneService.Get через ZoneRegistry. "" = глобальный пул (zone не нужна).
	if req.ZoneID != "" && s.zoneReg != nil {
		if _, err := s.zoneReg.Get(ctx, req.ZoneID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, status.Errorf(codes.FailedPrecondition, "unknown zone id '%s'", req.ZoneID)
			}
			return nil, mapRepoErr(err)
		}
	}
	p := &domain.AddressPool{
		ID:               ids.NewID("apl"), // 3-char prefix per YC convention
		Name:             req.Name,
		Description:      req.Description,
		Labels:           req.Labels,
		V4CIDRBlocks:     req.V4CIDRBlocks,
		V6CIDRBlocks:     req.V6CIDRBlocks,
		Kind:             req.Kind,
		ZoneID:           req.ZoneID,
		IsDefault:        req.IsDefault,
		SelectorLabels:   req.SelectorLabels,
		SelectorPriority: req.SelectorPriority,
		CreatedAt:        time.Now().UTC(),
	}
	p.ModifiedAt = p.CreatedAt
	created, err := s.pools.Insert(ctx, p)
	if err != nil {
		return nil, err
	}
	// Материализуем per-IP freelist только для V4CIDRBlocks (миграция 0015):
	// PG-native AllocateIPFromFreelist полагается на эту таблицу. v6-блоки
	// идут через sparse counter в ipv6_pool_cursors (см. ниже).
	// PopulateFreelistForPool сам читает только v4_cidr_blocks (KAC-71) —
	// для v4-only / dual-stack pool заполнит ровно v4-CIDR'ы; для v6-only
	// pool это no-op.
	if err := s.pools.PopulateFreelistForPool(ctx, created.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "populate freelist: %v", err)
	}
	// KAC-58: pool с IPv6 CIDR использует sparse counter-based allocator
	// (миграция 0020). Initialise next_offset=1 если pool имеет v6-блоки.
	if len(created.V6CIDRBlocks) > 0 {
		if err := s.addrRepo.InitIPv6PoolCursor(ctx, created.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
		}
	}
	return created, nil
}

// familyStrict — режим проверки family у CIDR-блока в split-shape.
type familyStrict int

const (
	familyV4Strict familyStrict = iota
	familyV6Strict
)

// validateAddressPoolCIDRs — REQ-IPL-CR-05 / REQ-IPL-CR-06: каждый блок в
// соответствующем слоте обязан быть нужной family + host-bits=0. Сообщения —
// verbatim из acceptance §0:
//   - `v4_cidr_blocks[N]: "..." is not an IPv4 prefix`
//   - `v6_cidr_blocks[N]: "..." is not an IPv6 prefix`
//   - `<field>[N]: "..." host bits must be zero (use ...)` — общая форма для
//     обеих family.
func validateAddressPoolCIDRs(field string, blocks []string, want familyStrict) error {
	for i, c := range blocks {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return status.Errorf(codes.InvalidArgument,
				"%s[%d]: %q is not a valid CIDR prefix: %v", field, i, c, err)
		}
		// Family-фильтр первым — иначе host-bits сообщение будет вводить в
		// заблуждение для cross-family-prefix'а.
		isV6 := p.Addr().Is6() && !p.Addr().Is4In6()
		switch want {
		case familyV4Strict:
			if isV6 {
				return status.Errorf(codes.InvalidArgument,
					"%s[%d]: %q is not an IPv4 prefix", field, i, c)
			}
		case familyV6Strict:
			if !isV6 {
				return status.Errorf(codes.InvalidArgument,
					"%s[%d]: %q is not an IPv6 prefix", field, i, c)
			}
		}
		// Host-bits должны быть 0 (canonical form: 198.51.100.0/24, не /5;
		// для v6 — 2001:db8::/64, не 2001:db8::5/64).
		if p.Masked() != p {
			return status.Errorf(codes.InvalidArgument,
				"%s[%d]: %q host bits must be zero (use %s)",
				field, i, c, p.Masked().String())
		}
	}
	return nil
}

func (s *AddressPoolService) Get(ctx context.Context, id string) (*domain.AddressPool, error) {
	return s.pools.Get(ctx, id)
}

func (s *AddressPoolService) List(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*domain.AddressPool, string, error) {
	return s.pools.List(ctx, f, p)
}

// UpdatePoolReq — частичное обновление; nil-пойнтеры/false-flags = no-op.
//
// KAC-71: CIDR-поля — детерминированный replace через explicit bool-флаги
// `ReplaceV4CIDR` / `ReplaceV6CIDR`. Body-array значимо ТОЛЬКО при выставленном
// флаге (даже пустой массив — это явный «очистить» при флаге true). Без флага
// V4CIDRBlocks / V6CIDRBlocks в запросе игнорируется (REQ-IPL-UPD-06 / B12).
// Это позволяет изменять один family, не трогая второй; и явно очищать family
// (превращая dual-stack pool в v4-only или v6-only). Очистить оба family
// одновременно (или единственный непустой family) запрещено invariant'ом
// "v4 ∪ v6 ≠ ∅ after update" (REQ-IPL-UPD-03 / B10).
type UpdatePoolReq struct {
	ID                     string
	Name                   *string
	Description            *string
	ReplaceLabels          bool
	Labels                 map[string]string
	ReplaceV4CIDR          bool
	V4CIDRBlocks           []string
	ReplaceV6CIDR          bool
	V6CIDRBlocks           []string
	UpdateIsDefault        bool
	IsDefault              bool
	ReplaceSelectorLabels  bool
	SelectorLabels         map[string]string
	UpdateSelectorPriority bool
	SelectorPriority       int32
}

func (s *AddressPoolService) Update(ctx context.Context, req UpdatePoolReq) (*domain.AddressPool, error) {
	cur, err := s.pools.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		cur.Name = *req.Name
	}
	if req.Description != nil {
		cur.Description = *req.Description
	}
	if req.ReplaceLabels {
		cur.Labels = req.Labels
	}
	// REQ-IPL-UPD-01 / B7 + REQ-IPL-UPD-02 / B8: replace выполняется ТОЛЬКО при
	// явном bool-флаге; пустой массив в запросе без флага игнорируется
	// (REQ-IPL-UPD-06 / B12). При флаге true — body-array становится новым
	// содержимым, включая пустой массив (REQ-IPL-UPD-05 / B11 — очистить один
	// family на dual-stack pool).
	newV4 := cur.V4CIDRBlocks
	newV6 := cur.V6CIDRBlocks
	if req.ReplaceV4CIDR {
		if err := validateAddressPoolCIDRs("v4_cidr_blocks", req.V4CIDRBlocks, familyV4Strict); err != nil {
			return nil, err
		}
		newV4 = req.V4CIDRBlocks
	}
	if req.ReplaceV6CIDR {
		if err := validateAddressPoolCIDRs("v6_cidr_blocks", req.V6CIDRBlocks, familyV6Strict); err != nil {
			return nil, err
		}
		newV6 = req.V6CIDRBlocks
	}
	// REQ-IPL-UPD-03 / B10: post-update invariant — хотя бы один family непуст.
	// Проверяем только если хотя бы один replace-флаг выставлен (без них state
	// не меняется и проверка не нужна).
	if req.ReplaceV4CIDR || req.ReplaceV6CIDR {
		if len(newV4) == 0 && len(newV6) == 0 {
			return nil, status.Error(codes.InvalidArgument,
				"v4_cidr_blocks and v6_cidr_blocks must not be both empty after update")
		}
	}
	v6Added := req.ReplaceV6CIDR && len(newV6) > 0 && len(cur.V6CIDRBlocks) == 0
	cur.V4CIDRBlocks = newV4
	cur.V6CIDRBlocks = newV6
	// KAC-58: если v6-family появилась на этом pool впервые (был v4-only,
	// стал dual-stack) — инициализируем cursor. InitIPv6PoolCursor идемпотентен,
	// поэтому повторная инициализация безопасна; но защищаем себя от
	// no-op-вызовов когда v6 не менялся.
	if v6Added {
		if err := s.addrRepo.InitIPv6PoolCursor(ctx, cur.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
		}
	}
	if req.UpdateIsDefault {
		cur.IsDefault = req.IsDefault
	}
	if req.ReplaceSelectorLabels {
		cur.SelectorLabels = req.SelectorLabels
	}
	if req.UpdateSelectorPriority {
		cur.SelectorPriority = req.SelectorPriority
	}
	cur.ModifiedAt = time.Now().UTC()
	return s.pools.Update(ctx, cur)
}

// Delete pool'а запрещён, если из него выделены IP (есть Address с
// external_ipv4.address_pool_id = id и непустым address). FK constraint
// невозможен (адрес ссылается через JSONB, не через колонку) —
// service-level guard обязателен. Closes: pool-delete-leaves-orphan-
// addresses bug.
//
// Bindings (network_default / address_override) удаляются автоматически
// через ON DELETE RESTRICT FK — они блокируют delete. Caller должен
// сначала Unbind.
func (s *AddressPoolService) Delete(ctx context.Context, id string) error {
	if _, err := s.pools.Get(ctx, id); err != nil {
		return err
	}
	n, err := s.pools.CountAddressesByPool(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"AddressPool %s is not empty (%d allocated addresses); release IPs first", id, n)
	}
	return s.pools.Delete(ctx, id)
}

// -- Bindings --

// BindAsNetworkDefault — назначить pool как default для Network.
func (s *AddressPoolService) BindAsNetworkDefault(ctx context.Context, networkID, poolID string) error {
	if _, err := s.netRepo.Get(ctx, networkID); err != nil {
		return err
	}
	if _, err := s.pools.Get(ctx, poolID); err != nil {
		return err
	}
	return s.bindings.SetNetworkDefault(ctx, networkID, poolID)
}

func (s *AddressPoolService) UnbindNetworkDefault(ctx context.Context, networkID string) error {
	return s.bindings.UnsetNetworkDefault(ctx, networkID)
}

// BindAsAddressOverride — назначить override-pool для Address.
// Возвращает FailedPrecondition если у Address уже выделен external IP.
func (s *AddressPoolService) BindAsAddressOverride(ctx context.Context, addressID, poolID string) error {
	a, err := s.addrRepo.Get(ctx, addressID)
	if err != nil {
		return err
	}
	if _, err := s.pools.Get(ctx, poolID); err != nil {
		return err
	}
	if a.ExternalIpv4 != nil && a.ExternalIpv4.Address != "" {
		return status.Errorf(codes.FailedPrecondition,
			"address %s already has allocated external IP %q; override would be a no-op",
			addressID, a.ExternalIpv4.Address)
	}
	return s.bindings.SetAddressOverride(ctx, addressID, poolID)
}

func (s *AddressPoolService) UnbindAddressOverride(ctx context.Context, addressID string) error {
	return s.bindings.UnsetAddressOverride(ctx, addressID)
}

// ResolvedPool — результат cascade-резолва, с указанием через какой шаг матчилось.
type ResolvedPool struct {
	Pool            *domain.AddressPool
	MatchedVia      string            // "address_override" | "network_default" | "label_selector" | "zone_default" | "global_default"
	MatchedSelector map[string]string // populated only for label_selector
}

// AddressFamily — IP-семейство для cascade-resolve фильтрации (KAC-63).
// Без явного family pool-cascade выберет default v4-пул и для v6-запроса,
// что приведёт к Internal "pool has no IPv6 cidr_blocks" в allocator'е.
type AddressFamily int

const (
	FamilyV4 AddressFamily = iota
	FamilyV6
)

// ResolvePoolForAddress — полный 5-step cascade:
//
//  1. address_pool_address_override        (explicit per-address)
//  2. address_pool_network_default         (explicit per-network; для internal IP)
//  3. cloud-label-selector ⊆ pool.selector_labels  (admin Cloud labels)
//  4. zone-default                         (is_default=true для zone+kind)
//  5. global-default                       (is_default=true для zone IS NULL и kind)
//
// Используется AllocateExternalIP. Если ни один шаг не дал результата —
// возвращает ErrNotFound (caller возвращает FailedPrecondition / ResourceExhausted).
func (s *AddressPoolService) ResolvePoolForAddress(ctx context.Context, addressID string) (*ResolvedPool, error) {
	res, _, err := s.resolveWithRunnerUp(ctx, addressID, "", domain.AddressPoolKindExternalPublic, FamilyV4)
	return res, err
}

// ResolvePoolForAddressObj — то же что ResolvePoolForAddress, но принимает
// уже полученный *domain.Address. Избегает повторного s.addrRepo.Get(addressID)
// в hot path AllocateExternalIP, который сам уже сделал Get.
//
// Fail-fast на nil: caller должен передать valid addr; nil-fallback на
// hypothetical resolve без addressID был бы silent degradation
// (теряем cascade Step 1 + folder-id для Step 3).
func (s *AddressPoolService) ResolvePoolForAddressObj(ctx context.Context, addr *domain.Address) (*ResolvedPool, error) {
	return s.ResolvePoolForAddressObjFamily(ctx, addr, FamilyV4)
}

// ResolvePoolForAddressObjFamily — cascade-resolve с явным IP-family фильтром (KAC-63).
// Каждый step отвергает pool без CIDR нужной family и проваливается на следующий step,
// чтобы default v4-пул не «утаскивал» v6-аллокацию (и наоборот).
func (s *AddressPoolService) ResolvePoolForAddressObjFamily(ctx context.Context, addr *domain.Address, family AddressFamily) (*ResolvedPool, error) {
	if addr == nil {
		return nil, status.Error(codes.InvalidArgument, "ResolvePoolForAddressObjFamily: addr is required (use ResolvePoolForAddress for ID-only path)")
	}
	res, _, err := s.doResolve(ctx, addr.ID, addr, "", domain.AddressPoolKindExternalPublic, family)
	return res, err
}

// poolHasFamily — true если pool имеет хотя бы один CIDR-блок запрошенной family.
//
// KAC-71: после split CIDR-блоков family-фильтр становится тривиальным
// `len(V4CIDRBlocks)>0` / `len(V6CIDRBlocks)>0` — без runtime-парсинга. Service-
// слой обеспечивает family-correctness на Create/Update (REQ-IPL-CR-05 / B6 +
// REQ-IPL-UPD-01/02), поэтому колонка является source-of-truth по family.
// Cascade `doResolve` использует это на всех 5 шагах единообразно — pool без
// требуемой family пропускается, cascade проваливается дальше (REQ-RESOLVE-06,
// REQ-RESOLVE-07).
func poolHasFamily(pool *domain.AddressPool, family AddressFamily) bool {
	if pool == nil {
		return false
	}
	switch family {
	case FamilyV6:
		return len(pool.V6CIDRBlocks) > 0
	default:
		return len(pool.V4CIDRBlocks) > 0
	}
}

// resolveWithRunnerUp — общая логика резолва, опционально вычисляет runner-up
// (для ExplainResolution). networkID может быть передан явно (hypothetical
// resolve когда address ещё не существует); если addressID непуст — он имеет
// приоритет (включая address-override).
//
// kindHint — какой kind пула caller ожидает. Phase 1 использует
// EXTERNAL_PUBLIC. Никаких kind-fallback'ов.
//
// Zone берётся напрямую из Address.external_ipv4.zone_id / external_ipv6.zone_id (для external)
// или Subnet.zone_id (для internal). Никаких regionFromZone-преобразований —
// AddressPool теперь привязан к zone, не к region (см. миграция 0020).
func (s *AddressPoolService) resolveWithRunnerUp(
	ctx context.Context,
	addressID, networkIDOverride string,
	kindHint domain.AddressPoolKind,
	family AddressFamily,
) (*ResolvedPool, *ResolvedPool, error) {
	return s.doResolve(ctx, addressID, nil, networkIDOverride, kindHint, family)
}

// doResolve — единая реализация cascade. Если preloadedAddr != nil — переиспользуется
// без дополнительного s.addrRepo.Get (устраняет double-Get в hot path).
//
// family (KAC-63) — pool должен иметь хотя бы один CIDR требуемой family.
// Pool не подходящий по family — отвергается на каждом step и cascade
// проваливается на следующий step, а не оборачивается в Internal в allocator'е.
func (s *AddressPoolService) doResolve(
	ctx context.Context,
	addressID string,
	preloadedAddr *domain.Address,
	networkIDOverride string,
	kindHint domain.AddressPoolKind,
	family AddressFamily,
) (*ResolvedPool, *ResolvedPool, error) {

	// Step 1: address_override.
	if addressID != "" {
		if poolID, err := s.bindings.GetAddressOverride(ctx, addressID); err == nil && poolID != "" {
			pool, err := s.pools.Get(ctx, poolID)
			if err == nil && poolHasFamily(pool, family) {
				return &ResolvedPool{Pool: pool, MatchedVia: "address_override"},
					s.tryRestForRunnerUp(ctx, addressID, networkIDOverride, kindHint, "address_override"),
					nil
			}
		}
	}

	// Resolve network_id, zone_id, folder_id из address-spec.
	// Используем preloadedAddr если есть; иначе делаем Get.
	networkID := networkIDOverride
	zoneID := ""
	folderID := ""
	if addressID != "" {
		a := preloadedAddr
		if a == nil {
			fetched, err := s.addrRepo.Get(ctx, addressID)
			if err != nil {
				return nil, nil, err
			}
			a = fetched
		}
		folderID = a.FolderID
		if a.ExternalIpv4 != nil && a.ExternalIpv4.ZoneID != "" {
			zoneID = a.ExternalIpv4.ZoneID
		}
		if a.ExternalIpv6 != nil && a.ExternalIpv6.ZoneID != "" {
			zoneID = a.ExternalIpv6.ZoneID
		}
		if a.InternalIpv4 != nil && a.InternalIpv4.SubnetID != "" {
			sub, err := s.subnetRepo.Get(ctx, a.InternalIpv4.SubnetID)
			if err == nil {
				networkID = sub.NetworkID
				if zoneID == "" && sub.ZoneID != "" {
					zoneID = sub.ZoneID
				}
			}
		}
	}

	// Step 2: network_default (только когда есть networkID — internal IP path).
	if networkID != "" {
		if poolID, err := s.bindings.GetNetworkDefault(ctx, networkID); err == nil && poolID != "" {
			pool, err := s.pools.Get(ctx, poolID)
			if err == nil && poolHasFamily(pool, family) {
				return &ResolvedPool{Pool: pool, MatchedVia: "network_default"},
					s.tryRestForRunnerUp(ctx, addressID, networkID, kindHint, "network_default"),
					nil
			}
		}
	}

	// Step 3: label-selector match через CloudPoolSelector.
	// folder_id → cloud_id → selector. Если folderClient не настроен или
	// folder не имеет cloud — skip step.
	if folderID != "" && s.folderClient != nil && s.cloudSel != nil {
		if cloudID, err := s.folderClient.GetCloudID(ctx, folderID); err == nil && cloudID != "" {
			if sel, err := s.cloudSel.Get(ctx, cloudID); err == nil && !sel.IsEmpty() {
				// Берём по 2 на каждый match чтобы после family-фильтра остался
				// шанс выбрать non-trivial pool.
				matches, err := s.pools.FindBySelectorMatch(ctx, sel.Selector, zoneID, kindHint, 4)
				if err == nil {
					filtered := matches[:0]
					for _, p := range matches {
						if poolHasFamily(p, family) {
							filtered = append(filtered, p)
						}
					}
					if len(filtered) > 0 {
						return &ResolvedPool{
							Pool:            filtered[0],
							MatchedVia:      "label_selector",
							MatchedSelector: sel.Selector,
						}, nilOrSecond(filtered), nil
					}
				}
			}
		}
	}

	// Step 4: zone_default — точный match по (zone, kind, family).
	if zoneID != "" {
		if pool, err := s.pools.GetDefaultForZone(ctx, zoneID, kindHint); err == nil && poolHasFamily(pool, family) {
			return &ResolvedPool{Pool: pool, MatchedVia: "zone_default"}, nil, nil
		}
	}

	// Step 5: global_default (zone_id IS NULL).
	if pool, err := s.pools.GetDefaultForZone(ctx, "", kindHint); err == nil && poolHasFamily(pool, family) {
		return &ResolvedPool{Pool: pool, MatchedVia: "global_default"}, nil, nil
	}

	return nil, nil, fmt.Errorf("%w for address %s (network %s, family=%d)", ErrPoolNotResolved, addressID, networkID, family)
}

func nilOrSecond(matches []*domain.AddressPool) *ResolvedPool {
	if len(matches) < 2 {
		return nil
	}
	return &ResolvedPool{Pool: matches[1], MatchedVia: "label_selector"}
}

// tryRestForRunnerUp — упрощённая попытка вычислить runner-up: skip step that
// won the primary, run cascade from next. Возвращает nil если ничего нет.
func (s *AddressPoolService) tryRestForRunnerUp(
	ctx context.Context, addressID, networkID string,
	kind domain.AddressPoolKind, skipStep string,
) *ResolvedPool {
	// Реализация runner-up для всего cascade нетривиальна; в Phase 1
	// возвращаем nil. Phase 2 — добавим полный recursion с skip-mask.
	_ = ctx
	_ = addressID
	_ = networkID
	_ = kind
	_ = skipStep
	return nil
}

// ExplainResolution — публичный метод для InternalAddressPoolService.ExplainResolution RPC.
// Возвращает primary + runner-up (если есть). Family определяется по spec самого address:
// external_ipv6 → FamilyV6; иначе → FamilyV4 (KAC-63).
func (s *AddressPoolService) ExplainResolution(ctx context.Context, addressID, networkID string) (*ResolvedPool, *ResolvedPool, error) {
	family := FamilyV4
	if addressID != "" {
		if a, err := s.addrRepo.Get(ctx, addressID); err == nil && a.ExternalIpv6 != nil {
			family = FamilyV6
		}
	}
	return s.resolveWithRunnerUp(ctx, addressID, networkID, domain.AddressPoolKindExternalPublic, family)
}

// Check — диагностика IPAM-конфигурации. Возвращает list of warnings.
// Не блокирует и не модифицирует state.
func (s *AddressPoolService) Check(ctx context.Context, zoneID string) ([]string, error) {
	groups, err := s.pools.FindAmbiguousSelectorGroups(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		ids := make([]string, 0, len(g))
		for _, p := range g {
			ids = append(ids, p.ID)
		}
		warnings = append(warnings, fmt.Sprintf(
			"%d pools share identical (zone_id, kind, selector_labels, selector_priority) — resolve order undefined: %v. Set distinct selector_priority to disambiguate.",
			len(g), ids,
		))
	}
	return warnings, nil
}

// -- Admin observability (utilization + cross-folder addresses) --

// PoolUtilization — расчётная статистика использования pool'а.
type PoolUtilization struct {
	PoolID      string
	TotalIPs    int64
	UsedIPs     int64
	FreeIPs     int64
	UsedPercent int32
	CIDRs       []CIDRUsage
}

// CIDRUsage — usage конкретного CIDR-блока внутри pool'а.
type CIDRUsage struct {
	CIDR  string
	Total int64
	Used  int64
}

// GetPoolUtilization — total/used/free + per-CIDR breakdown. Admin-only.
func (s *AddressPoolService) GetPoolUtilization(ctx context.Context, poolID string) (*PoolUtilization, error) {
	pool, err := s.pools.Get(ctx, poolID)
	if err != nil {
		return nil, err
	}
	perCIDR, err := s.pools.CountAddressesByPoolPerCIDR(ctx, poolID)
	if err != nil {
		return nil, err
	}
	out := &PoolUtilization{PoolID: poolID}
	// KAC-71: utilization считается ТОЛЬКО для V4CIDRBlocks (sparse v6-allocator
	// ведёт свою бухгалтерию через ipv6_pool_cursors / ipv6_allocated_ips —
	// отдельный observability path). Чтобы admin-UI видел v6-CIDR'ы в списке,
	// добавляем их с Total=Used=0 (placeholder, реальная v6-стата — TBD).
	for _, c := range pool.V4CIDRBlocks {
		total := usableIPv4Count(c)
		used := perCIDR[c]
		out.CIDRs = append(out.CIDRs, CIDRUsage{CIDR: c, Total: total, Used: used})
		out.TotalIPs += total
		out.UsedIPs += used
	}
	for _, c := range pool.V6CIDRBlocks {
		out.CIDRs = append(out.CIDRs, CIDRUsage{CIDR: c, Total: 0, Used: 0})
	}
	out.FreeIPs = out.TotalIPs - out.UsedIPs
	if out.FreeIPs < 0 {
		out.FreeIPs = 0
	}
	if out.TotalIPs > 0 {
		out.UsedPercent = int32(out.UsedIPs * 100 / out.TotalIPs)
	}
	return out, nil
}

// ListPoolAddresses — кросс-folder список Address с IP из pool.
func (s *AddressPoolService) ListPoolAddresses(ctx context.Context, poolID, folderFilter string, p Pagination) ([]*domain.Address, string, error) {
	if poolID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "pool_id required")
	}
	return s.pools.ListAddressesByPool(ctx, poolID, folderFilter, p)
}

// usableIPv4Count — usable IPs в CIDR (исключая network+broadcast).
// Для /N: 2^(32-N) - 2; для /31: 2 (RFC 3021); для /32: 1.
// Если CIDR невалиден — 0.
func usableIPv4Count(cidr string) int64 {
	p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil || !p.Addr().Is4() {
		return 0
	}
	bits := p.Bits()
	if bits == 32 {
		return 1
	}
	if bits == 31 {
		return 2
	}
	hostBits := 32 - bits
	if hostBits >= 31 {
		return 0
	}
	return int64(1)<<hostBits - 2
}

// -- CloudPoolSelector wrappers (для InternalCloudService.{Set,Unset,Get}PoolSelector) --

// SetCloudPoolSelector — admin-устанавливает selector на Cloud. Cloud должен
// существовать в kacho-resource-manager.
func (s *AddressPoolService) SetCloudPoolSelector(ctx context.Context, cloudID string, selector map[string]string, setBy string) error {
	if cloudID == "" {
		return status.Error(codes.InvalidArgument, "cloud_id required")
	}
	return s.cloudSel.Set(ctx, cloudID, selector, setBy)
}

func (s *AddressPoolService) UnsetCloudPoolSelector(ctx context.Context, cloudID string) error {
	if cloudID == "" {
		return status.Error(codes.InvalidArgument, "cloud_id required")
	}
	return s.cloudSel.Unset(ctx, cloudID)
}

func (s *AddressPoolService) GetCloudPoolSelector(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	return s.cloudSel.Get(ctx, cloudID)
}
