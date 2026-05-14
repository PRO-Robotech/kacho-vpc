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
type CreatePoolReq struct {
	Name             string
	Description      string
	Labels           map[string]string
	CIDRBlocks       []string
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
	if len(req.CIDRBlocks) == 0 {
		return nil, status.Error(codes.InvalidArgument, "cidr_blocks must contain at least one prefix")
	}
	hasV6 := false
	for _, c := range req.CIDRBlocks {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid cidr %q: %v", c, err)
		}
		// IPv4 — materialised freelist allocator (миграция 0014).
		// IPv6 — sparse counter-based allocator (KAC-58, миграция 0020).
		// Mixed-family pools допустимы (allocator выбирается по ip_version
		// в request-spec); cascade resolve фильтрует pools по family.
		if p.Addr().Is6() {
			hasV6 = true
		}
		// Host-bits должны быть 0 (canonical form: 198.51.100.0/24, не /5).
		// Для v6: например 2001:db8::/64 — без host-bits.
		if p.Masked() != p {
			return nil, status.Errorf(codes.InvalidArgument,
				"cidr %q: host bits must be zero (use %s)", c, p.Masked().String())
		}
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
		CIDRBlocks:       req.CIDRBlocks,
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
	// Материализуем per-IP freelist для IPv4 CIDR-блоков pool'а (миграция 0014):
	// PG-native AllocateIPFromFreelist полагается на эту таблицу. IPv6 CIDR'ы
	// PopulateFreelistForPool пропускает (для них — sparse counter в
	// ipv6_pool_cursors, инициализируется ниже).
	if err := s.pools.PopulateFreelistForPool(ctx, created.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "populate freelist: %v", err)
	}
	// KAC-58: pool с IPv6 CIDR использует sparse counter-based allocator
	// (миграция 0020). Initialise next_offset=1 для каждого такого pool.
	if hasV6 {
		if err := s.addrRepo.InitIPv6PoolCursor(ctx, created.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
		}
	}
	return created, nil
}

func (s *AddressPoolService) Get(ctx context.Context, id string) (*domain.AddressPool, error) {
	return s.pools.Get(ctx, id)
}

func (s *AddressPoolService) List(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*domain.AddressPool, string, error) {
	return s.pools.List(ctx, f, p)
}

// UpdatePoolReq — частичное обновление; nil-пойнтеры/false-flags = no-op.
type UpdatePoolReq struct {
	ID                     string
	Name                   *string
	Description            *string
	ReplaceLabels          bool
	Labels                 map[string]string
	ReplaceCIDR            bool
	CIDRBlocks             []string
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
	if req.ReplaceCIDR {
		hasV6Update := false
		for _, c := range req.CIDRBlocks {
			p, err := netip.ParsePrefix(strings.TrimSpace(c))
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid cidr %q: %v", c, err)
			}
			if p.Addr().Is6() {
				hasV6Update = true
			}
			if p.Masked() != p {
				return nil, status.Errorf(codes.InvalidArgument,
					"cidr %q: host bits must be zero (use %s)", c, p.Masked().String())
			}
		}
		cur.CIDRBlocks = req.CIDRBlocks
		// KAC-58: если новый CIDR-набор включает v6 — initialise cursor
		// (idempotent — InitIPv6PoolCursor использует ON CONFLICT DO NOTHING).
		if hasV6Update {
			if err := s.addrRepo.InitIPv6PoolCursor(ctx, cur.ID); err != nil {
				return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
			}
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
	res, _, err := s.resolveWithRunnerUp(ctx, addressID, "", domain.AddressPoolKindExternalPublic)
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
	if addr == nil {
		return nil, status.Error(codes.InvalidArgument, "ResolvePoolForAddressObj: addr is required (use ResolvePoolForAddress for ID-only path)")
	}
	res, _, err := s.doResolve(ctx, addr.ID, addr, "", domain.AddressPoolKindExternalPublic)
	return res, err
}

// resolveWithRunnerUp — общая логика резолва, опционально вычисляет runner-up
// (для ExplainResolution). networkID может быть передан явно (hypothetical
// resolve когда address ещё не существует); если addressID непуст — он имеет
// приоритет (включая address-override).
//
// kindHint — какой kind пула caller ожидает. Phase 1 использует
// EXTERNAL_PUBLIC. Никаких kind-fallback'ов.
//
// Zone берётся напрямую из Address.external_ipv4.zone_id (для external) или
// Subnet.zone_id (для internal). Никаких regionFromZone-преобразований —
// AddressPool теперь привязан к zone, не к region (см. миграция 0020).
func (s *AddressPoolService) resolveWithRunnerUp(
	ctx context.Context,
	addressID, networkIDOverride string,
	kindHint domain.AddressPoolKind,
) (*ResolvedPool, *ResolvedPool, error) {
	return s.doResolve(ctx, addressID, nil, networkIDOverride, kindHint)
}

// doResolve — единая реализация cascade. Если preloadedAddr != nil — переиспользуется
// без дополнительного s.addrRepo.Get (устраняет double-Get в hot path).
func (s *AddressPoolService) doResolve(
	ctx context.Context,
	addressID string,
	preloadedAddr *domain.Address,
	networkIDOverride string,
	kindHint domain.AddressPoolKind,
) (*ResolvedPool, *ResolvedPool, error) {

	// Step 1: address_override.
	if addressID != "" {
		if poolID, err := s.bindings.GetAddressOverride(ctx, addressID); err == nil && poolID != "" {
			pool, err := s.pools.Get(ctx, poolID)
			if err == nil {
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
			if err == nil {
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
				matches, err := s.pools.FindBySelectorMatch(ctx, sel.Selector, zoneID, kindHint, 2)
				if err == nil && len(matches) > 0 {
					return &ResolvedPool{
						Pool:            matches[0],
						MatchedVia:      "label_selector",
						MatchedSelector: sel.Selector,
					}, nilOrSecond(matches), nil
				}
			}
		}
	}

	// Step 4: zone_default — точный match по (zone, kind).
	if zoneID != "" {
		if pool, err := s.pools.GetDefaultForZone(ctx, zoneID, kindHint); err == nil {
			return &ResolvedPool{Pool: pool, MatchedVia: "zone_default"}, nil, nil
		}
	}

	// Step 5: global_default (zone_id IS NULL).
	if pool, err := s.pools.GetDefaultForZone(ctx, "", kindHint); err == nil {
		return &ResolvedPool{Pool: pool, MatchedVia: "global_default"}, nil, nil
	}

	return nil, nil, fmt.Errorf("%w for address %s (network %s)", ErrPoolNotResolved, addressID, networkID)
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
// Возвращает primary + runner-up (если есть).
func (s *AddressPoolService) ExplainResolution(ctx context.Context, addressID, networkID string) (*ResolvedPool, *ResolvedPool, error) {
	return s.resolveWithRunnerUp(ctx, addressID, networkID, domain.AddressPoolKindExternalPublic)
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
	for _, c := range pool.CIDRBlocks {
		total := usableIPv4Count(c)
		used := perCIDR[c]
		out.CIDRs = append(out.CIDRs, CIDRUsage{CIDR: c, Total: total, Used: used})
		out.TotalIPs += total
		out.UsedIPs += used
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
