package addresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// ExplainResolutionUseCase — admin diagnostic «куда попадёт?».
// Возвращает primary + runner-up (если есть). Family определяется по spec
// самого address: external_ipv6 → FamilyV6; иначе → FamilyV4 (KAC-63).
//
// REQ-RESOLVE-04 / D4: при fall-through (никаких pool требуемой family)
// возвращаем ErrPoolNotResolved — handler конвертирует это в HTTP 200 +
// `matched_via="none"` (см. handler.go::ExplainResolution).
type ExplainResolutionUseCase struct {
	addrRepo AddressRepo
	resolver *ResolverService
}

// NewExplainResolutionUseCase собирает use-case.
func NewExplainResolutionUseCase(addrRepo AddressRepo, resolver *ResolverService) *ExplainResolutionUseCase {
	return &ExplainResolutionUseCase{addrRepo: addrRepo, resolver: resolver}
}

// Execute возвращает primary + runner-up cascade результат.
func (u *ExplainResolutionUseCase) Execute(ctx context.Context, addressID, networkID string) (*ResolvedPool, *ResolvedPool, error) {
	family := FamilyV4
	if addressID != "" {
		if a, err := u.addrRepo.Get(ctx, addressID); err == nil && a.ExternalIpv6 != nil {
			family = FamilyV6
		}
	}
	return u.resolver.resolveWithRunnerUp(ctx, addressID, networkID, domain.AddressPoolKindExternalPublic, family)
}
