// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListAddressesUseCase — список адресов с пагинацией. project_id обязателен —
// чтобы закрыть cross-project enumeration. Использует CQRS Reader.
//
// Per-object filtered List через FGA ListObjects (relation viewer; read==enforce).
// filter==nil / subject=="" → passthrough.
type ListAddressesUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListAddressesUseCase создает ListAddressesUseCase. filter может быть nil.
func NewListAddressesUseCase(r Repo, filter ListFilter) *ListAddressesUseCase {
	return &ListAddressesUseCase{repo: r, filter: filter}
}

// Execute — project_id required + per-object FGA-filter + load UsedBy.
// pagination ПОСЛЕ фильтра; bypass → repo.List; empty grant → пустой (no-leak);
// iam недоступен → fail-closed Unavailable.
func (u *ListAddressesUseCase) Execute(ctx context.Context, subjectID string, f AddressFilter, p Pagination) ([]*kachorepo.AddressRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = r.Close() }()

	addrs, nextToken, err := u.listFiltered(ctx, r, subjectID, f, p)
	if err != nil {
		return nil, "", err
	}
	loadUsedBy(ctx, r.Addresses(), addrs)
	return addrs, nextToken, nil
}

// listFiltered применяет per-object фильтр и делает соответствующий repo-вызов.
func (u *ListAddressesUseCase) listFiltered(ctx context.Context, r Reader, subjectID string, f AddressFilter, p Pagination) ([]*kachorepo.AddressRecord, string, error) {
	if u.filter == nil || subjectID == "" {
		addrs, next, lerr := r.Addresses().List(ctx, f, p)
		return addrs, next, serviceerr.MapRepoErr(lerr)
	}
	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeAddress, authzfilter.ActionAddressList)
	if ferr != nil {
		return nil, "", ferr
	}
	if bypass {
		addrs, next, lerr := r.Addresses().List(ctx, f, p)
		return addrs, next, serviceerr.MapRepoErr(lerr)
	}
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	addrs, next, lerr := r.Addresses().ListByIDs(ctx, f, allowedIDs, p)
	return addrs, next, serviceerr.MapRepoErr(lerr)
}

// ListBySubnetUseCase — child-list адресов конкретной подсети. Использует
// SubnetReader.AddressesBySubnet (join через internal_ipv4.subnet_id ИЛИ
// internal_ipv6.subnet_id).
type ListBySubnetUseCase struct {
	repo         Repo
	subnetReader SubnetReader
}

// NewListBySubnetUseCase создает ListBySubnetUseCase.
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
		return nil, "", serviceerr.MapRepoErr(err)
	}
	addrs, nextToken, err := u.subnetReader.AddressesBySubnet(ctx, subnetID, p)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
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

// NewListOperationsUseCase создает ListOperationsUseCase.
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
