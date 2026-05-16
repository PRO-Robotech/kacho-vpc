package subnet

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListUsedAddressesUseCase — возвращает Address-ресурсы, привязанные к подсети
// (через internal_ipv4.subnet_id) + referrer-записи (кто использует адрес,
// map address-id → reference; ключ отсутствует если referrer'а нет). Sync RPC,
// не Operation.
//
// Wave 5 replicate (KAC-94): использует CQRS Reader для existence + AddressesBySubnet.
type ListUsedAddressesUseCase struct {
	repo        Repo
	addrRefRepo AddressRefRepo // optional → references[] пуст (graceful degradation)
}

// NewListUsedAddressesUseCase создаёт ListUsedAddressesUseCase. `addrRefRepo`
// опционален (nil → references[] пуст).
func NewListUsedAddressesUseCase(r Repo, addrRefRepo AddressRefRepo) *ListUsedAddressesUseCase {
	return &ListUsedAddressesUseCase{repo: r, addrRefRepo: addrRefRepo}
}

// Execute — id-валидация + existence + AddressesBySubnet + (optional)
// referrer-обогащение.
func (u *ListUsedAddressesUseCase) Execute(ctx context.Context, subnetID string, p Pagination) ([]*kachorepo.AddressRecord, map[string]*domain.AddressReference, string, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, subnetID); err != nil {
		return nil, nil, "", err
	}
	if subnetID == "" {
		return nil, nil, "", status.Error(codes.InvalidArgument, "subnet_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	if _, err := rd.Subnets().Get(ctx, subnetID); err != nil {
		return nil, nil, "", mapRepoErr(err)
	}
	addrs, nextToken, err := rd.Subnets().AddressesBySubnet(ctx, subnetID, p)
	if err != nil {
		return nil, nil, "", mapRepoErr(err)
	}
	refs := map[string]*domain.AddressReference{}
	if u.addrRefRepo != nil && len(addrs) > 0 {
		idsList := make([]string, 0, len(addrs))
		for _, a := range addrs {
			idsList = append(idsList, a.ID)
		}
		refs, err = u.addrRefRepo.ReferencesForAddresses(ctx, idsList)
		if err != nil {
			return nil, nil, "", mapRepoErr(err)
		}
	}
	return addrs, refs, nextToken, nil
}
