package addresspool

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ResolverService — cascade-resolve движок AddressPool. Используется
// напрямую `apps/kacho/api/address/*` use-case'ами (через port `PoolService` в
// address-pkg ports.go) — Address.Create / Allocate*IP проводят resolve чтобы
// выяснить, из какого pool брать IP.
//
// Это **не handler-уровневый use-case**, а сервис-инфраструктурный (часть
// pool-доменной бизнес-логики, переиспользуемая Address use-case'ами).
// Skill evgeniy §2 B.2: «UseCase'ы локальны; не требуют доп.слоя» — для
// reusable-логики между несколькими use-case'ами одного домена это нормально
// держать как struct-сервис (а не plain-функцию) ради переиспользования
// dependency-injection и тестируемости.
type ResolverService struct {
	pools        AddressPoolRepo
	bindings     AddressPoolBindingRepo
	cloudSel     CloudPoolSelectorRepo
	addrRepo     AddressRepo
	subnetRepo   SubnetReader
	folderClient FolderClient // nil → step 3 (label-selector) пропускается
}

// NewResolverService собирает cascade-resolve движок.
func NewResolverService(
	pools AddressPoolRepo,
	bindings AddressPoolBindingRepo,
	cloudSel CloudPoolSelectorRepo,
	addrRepo AddressRepo,
	subnetRepo SubnetReader,
	folderClient FolderClient,
) *ResolverService {
	return &ResolverService{
		pools: pools, bindings: bindings, cloudSel: cloudSel,
		addrRepo: addrRepo, subnetRepo: subnetRepo,
		folderClient: folderClient,
	}
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
// возвращает ErrPoolNotResolved (caller возвращает FailedPrecondition / ResourceExhausted).
func (s *ResolverService) ResolvePoolForAddress(ctx context.Context, addressID string) (*ResolvedPool, error) {
	res, _, err := s.resolveWithRunnerUp(ctx, addressID, "", domain.AddressPoolKindExternalPublic, FamilyV4)
	return res, err
}

// ResolvePoolForAddressObj — то же что ResolvePoolForAddress, но принимает
// уже полученный *kachorepo.AddressRecord. Избегает повторного s.addrRepo.Get(addressID)
// в hot path AllocateExternalIP, который сам уже сделал Get.
//
// Fail-fast на nil: caller должен передать valid addr; nil-fallback на
// hypothetical resolve без addressID был бы silent degradation
// (теряем cascade Step 1 + folder-id для Step 3).
func (s *ResolverService) ResolvePoolForAddressObj(ctx context.Context, addr *kachorepo.AddressRecord) (*ResolvedPool, error) {
	return s.ResolvePoolForAddressObjFamily(ctx, addr, FamilyV4)
}

// ResolvePoolForAddressObjFamily — cascade-resolve с явным IP-family фильтром (KAC-63).
// Каждый step отвергает pool без CIDR нужной family и проваливается на следующий step,
// чтобы default v4-пул не «утаскивал» v6-аллокацию (и наоборот).
func (s *ResolverService) ResolvePoolForAddressObjFamily(ctx context.Context, addr *kachorepo.AddressRecord, family AddressFamily) (*ResolvedPool, error) {
	if addr == nil {
		return nil, status.Error(codes.InvalidArgument, "ResolvePoolForAddressObjFamily: addr is required (use ResolvePoolForAddress for ID-only path)")
	}
	res, _, err := s.doResolve(ctx, addr.ID, &addr.Address, "", domain.AddressPoolKindExternalPublic, family)
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
// Zone берётся напрямую из Address.external_ipv4.zone_id / external_ipv6.zone_id (для external)
// или Subnet.zone_id (для internal). Никаких regionFromZone-преобразований —
// AddressPool теперь привязан к zone, не к region (см. миграция 0020).
func (s *ResolverService) resolveWithRunnerUp(
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
func (s *ResolverService) doResolve(
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
			a = &fetched.Address
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
//
// В Phase 1 возвращает nil — реализация полного recursion с skip-mask
// отложена (legacy parity).
func (s *ResolverService) tryRestForRunnerUp(
	ctx context.Context, addressID, networkID string,
	kind domain.AddressPoolKind, skipStep string,
) *ResolvedPool {
	_ = ctx
	_ = addressID
	_ = networkID
	_ = kind
	_ = skipStep
	return nil
}
