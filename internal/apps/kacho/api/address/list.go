package address

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListAddressesUseCase — list addresses with pagination. folder_id обязателен
// (R10 #C1 closure — закрыт cross-folder enumeration).
//
// A.7 sub-PR 2 (KAC-94): использует CQRS Reader.
type ListAddressesUseCase struct {
	repo Repo
}

// NewListAddressesUseCase создаёт ListAddressesUseCase.
func NewListAddressesUseCase(r Repo) *ListAddressesUseCase {
	return &ListAddressesUseCase{repo: r}
}

// Execute — folder_id required + load UsedBy для каждого адреса.
func (u *ListAddressesUseCase) Execute(ctx context.Context, f AddressFilter, p Pagination) ([]*kachorepo.AddressRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	addrs, nextToken, err := r.Addresses().List(ctx, f, p)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	loadUsedBy(ctx, r.Addresses(), addrs)
	return addrs, nextToken, nil
}

// ListBySubnetUseCase — child-list addresses for subnet. Использует
// SubnetReader.AddressesBySubnet (joining через internal_ipv4.subnet_id ИЛИ
// internal_ipv6.subnet_id — миграция 0013 для v6 parity).
type ListBySubnetUseCase struct {
	repo         Repo
	subnetReader SubnetReader
}

// NewListBySubnetUseCase создаёт ListBySubnetUseCase.
func NewListBySubnetUseCase(r Repo, subnetReader SubnetReader) *ListBySubnetUseCase {
	return &ListBySubnetUseCase{repo: r, subnetReader: subnetReader}
}

// Execute — id-валидация → existence-check (Subnet) → AddressesBySubnet → UsedBy.
func (u *ListBySubnetUseCase) Execute(ctx context.Context, subnetID string, p Pagination) ([]*kachorepo.AddressRecord, string, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, subnetID); err != nil {
		return nil, "", err
	}
	if subnetID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if _, err := u.subnetReader.Get(ctx, subnetID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	addrs, nextToken, err := u.subnetReader.AddressesBySubnet(ctx, subnetID, p)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return addrs, nextToken, nil
	}
	defer func() { _ = r.Close() }()
	loadUsedBy(ctx, r.Addresses(), addrs)
	return addrs, nextToken, nil
}

// ListOperationsUseCase — операции, относящиеся к конкретному address-id.
// NB: без repo.Get-precondition — операции должны быть доступны и после Delete
// (история).
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — id-валидация (любой prefix принимается; ListOperations используется
// и сразу после Delete, поэтому existence-check не делаем) + list.
func (u *ListOperationsUseCase) Execute(ctx context.Context, addressID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("address", ids.PrefixAddress, addressID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: addressID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
