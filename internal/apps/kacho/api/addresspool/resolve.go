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
// address-pkg iface.go) — Address.Create / Allocate*IP проводят resolve чтобы
// выяснить, из какого pool брать IP.
//
// Это **не handler-уровневый use-case**, а сервис-инфраструктурный (часть
// pool-доменной бизнес-логики, переиспользуемая Address use-case'ами).
//
// Wave 5 A.7 sub-PR 1/6 (skill evgeniy §6 G.4): cascade — чистое read-path.
// На каждый вызов открывается **одна** read-TX `kacho.Repository.Reader(ctx)`
// — все 5 шагов cascade видят consistent snapshot (read-committed на slave).
// Прежняя схема (несколько `*Repo.Get` на разных pgxpool-conn'ах) допускала
// inconsistent view при concurrent admin write'ах; теперь cascade-snapshot
// атомарен в рамках TX.
type ResolverService struct {
	repo          Repo
	addrRepo      AddressRepo
	subnetRepo    SubnetReader
	projectClient ProjectClient // nil → step 3 (label-selector) пропускается
}

// NewResolverService собирает cascade-resolve движок.
func NewResolverService(
	r Repo,
	addrRepo AddressRepo,
	subnetRepo SubnetReader,
	projectClient ProjectClient,
) *ResolverService {
	return &ResolverService{
		repo: r, addrRepo: addrRepo, subnetRepo: subnetRepo,
		projectClient: projectClient,
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
// (для ExplainResolution).
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
func (s *ResolverService) doResolve(
	ctx context.Context,
	addressID string,
	preloadedAddr *domain.Address,
	networkIDOverride string,
	kindHint domain.AddressPoolKind,
	family AddressFamily,
) (*ResolvedPool, *ResolvedPool, error) {

	rd, err := s.repo.Reader(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rd.Close() }()

	// Step 1: address_override.
	if addressID != "" {
		if poolID, gerr := rd.AddressPoolBindings().GetAddressOverride(ctx, addressID); gerr == nil && poolID != "" {
			pool, gerr := rd.AddressPools().Get(ctx, poolID)
			if gerr == nil && poolHasFamilyRec(pool, family) {
				return &ResolvedPool{Pool: &pool.AddressPool, MatchedVia: "address_override"},
					s.tryRestForRunnerUp(ctx, addressID, networkIDOverride, kindHint, "address_override"),
					nil
			}
		}
	}

	// Resolve network_id, zone_id, project_id из address-spec.
	networkID := networkIDOverride
	zoneID := ""
	folderID := ""
	if addressID != "" {
		a := preloadedAddr
		if a == nil {
			fetched, gerr := s.addrRepo.Get(ctx, addressID)
			if gerr != nil {
				return nil, nil, gerr
			}
			a = &fetched.Address
		}
		folderID = a.ProjectID
		if a.ExternalIpv4 != nil && a.ExternalIpv4.ZoneID != "" {
			zoneID = a.ExternalIpv4.ZoneID
		}
		if a.ExternalIpv6 != nil && a.ExternalIpv6.ZoneID != "" {
			zoneID = a.ExternalIpv6.ZoneID
		}
		if a.InternalIpv4 != nil && a.InternalIpv4.SubnetID != "" {
			sub, gerr := s.subnetRepo.Get(ctx, a.InternalIpv4.SubnetID)
			if gerr == nil {
				networkID = sub.NetworkID
				if zoneID == "" && sub.ZoneID != "" {
					zoneID = sub.ZoneID
				}
			}
		}
	}

	// Step 2: network_default (только когда есть networkID — internal IP path).
	if networkID != "" {
		if poolID, gerr := rd.AddressPoolBindings().GetNetworkDefault(ctx, networkID); gerr == nil && poolID != "" {
			pool, gerr := rd.AddressPools().Get(ctx, poolID)
			if gerr == nil && poolHasFamilyRec(pool, family) {
				return &ResolvedPool{Pool: &pool.AddressPool, MatchedVia: "network_default"},
					s.tryRestForRunnerUp(ctx, addressID, networkID, kindHint, "network_default"),
					nil
			}
		}
	}

	// Step 3: label-selector match через CloudPoolSelector.
	if folderID != "" && s.projectClient != nil {
		if cloudID, gerr := s.projectClient.GetCloudIDFromProject(ctx, folderID); gerr == nil && cloudID != "" {
			if sel, gerr := rd.CloudPoolSelectors().Get(ctx, cloudID); gerr == nil && !sel.IsEmpty() {
				// Берём по 4 на каждый match чтобы после family-фильтра остался
				// шанс выбрать non-trivial pool.
				matches, mErr := rd.AddressPools().FindBySelectorMatch(ctx, sel.Selector, zoneID, kindHint, 4)
				if mErr == nil {
					filtered := matches[:0]
					for _, p := range matches {
						if poolHasFamilyRec(p, family) {
							filtered = append(filtered, p)
						}
					}
					if len(filtered) > 0 {
						return &ResolvedPool{
							Pool:            &filtered[0].AddressPool,
							MatchedVia:      "label_selector",
							MatchedSelector: sel.Selector,
						}, nilOrSecondRec(filtered), nil
					}
				}
			}
		}
	}

	// Step 4: zone_default — точный match по (zone, kind, family).
	if zoneID != "" {
		if pool, gerr := rd.AddressPools().GetDefaultForZone(ctx, zoneID, kindHint); gerr == nil && poolHasFamilyRec(pool, family) {
			return &ResolvedPool{Pool: &pool.AddressPool, MatchedVia: "zone_default"}, nil, nil
		}
	}

	// Step 5: global_default (zone_id IS NULL).
	if pool, gerr := rd.AddressPools().GetDefaultForZone(ctx, "", kindHint); gerr == nil && poolHasFamilyRec(pool, family) {
		return &ResolvedPool{Pool: &pool.AddressPool, MatchedVia: "global_default"}, nil, nil
	}

	return nil, nil, fmt.Errorf("%w for address %s (network %s, family=%d)", ErrPoolNotResolved, addressID, networkID, family)
}

// poolHasFamilyRec — family-фильтр для Record-обёртки (KAC-63).
func poolHasFamilyRec(rec *kachorepo.AddressPoolRecord, family AddressFamily) bool {
	if rec == nil {
		return false
	}
	return poolHasFamily(&rec.AddressPool, family)
}

// nilOrSecondRec возвращает второй match как runner-up ResolvedPool (nil если < 2).
func nilOrSecondRec(matches []*kachorepo.AddressPoolRecord) *ResolvedPool {
	if len(matches) < 2 {
		return nil
	}
	return &ResolvedPool{Pool: &matches[1].AddressPool, MatchedVia: "label_selector"}
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
