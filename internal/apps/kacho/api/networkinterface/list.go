// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/operations"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListNetworkInterfacesUseCase — список NIC'ов; project_id обязателен. Читает
// через reader-TX CQRS-интерфейса. Результат фильтруется per-object через FGA
// ListObjects (relation viewer, read==enforce): возвращаем только разрешенные
// subject'у NIC'и. filter==nil / subject=="" → passthrough без фильтра.
type ListNetworkInterfacesUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListNetworkInterfacesUseCase создает ListNetworkInterfacesUseCase. filter==nil OK.
func NewListNetworkInterfacesUseCase(r Repo, filter ListFilter) *ListNetworkInterfacesUseCase {
	return &ListNetworkInterfacesUseCase{repo: r, filter: filter}
}

// Execute — требует project_id и применяет per-object FGA-фильтр. Пагинация идет
// ПОСЛЕ фильтра; bypass → repo.List; пустой grant → пустой результат (no-leak);
// iam недоступен → fail-closed Unavailable.
func (u *ListNetworkInterfacesUseCase) Execute(ctx context.Context, subjectID string, f NetworkInterfaceFilter, p Pagination) ([]*kachorepo.NetworkInterfaceRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	if subjectID == "" && u.filter != nil {
		// identity не извлечен (anon) при включенном фильтре → fail-closed (no-leak).
		return nil, "", nil
	}
	if u.filter == nil || subjectID == authzfilter.SystemSubject {
		out, next, lerr := rd.NetworkInterfaces().List(ctx, f, p)
		if lerr != nil {
			return nil, "", serviceerr.MapRepoErr(lerr)
		}
		return out, next, nil
	}
	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeNetworkInterface, authzfilter.ActionNetworkInterfaceList)
	if ferr != nil {
		return nil, "", ferr
	}
	if bypass {
		out, next, lerr := rd.NetworkInterfaces().List(ctx, f, p)
		if lerr != nil {
			return nil, "", serviceerr.MapRepoErr(lerr)
		}
		return out, next, nil
	}
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	out, next, lerr := rd.NetworkInterfaces().ListByIDs(ctx, f, allowedIDs, p)
	if lerr != nil {
		return nil, "", serviceerr.MapRepoErr(lerr)
	}
	return out, next, nil
}

// ListOperationsUseCase — операции, относящиеся к конкретному NIC.
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создает ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — валидирует id и отдает список операций. Прекондишена repo.Get здесь
// нет специально: история операций должна оставаться доступной и после удаления
// ресурса (строки operations не привязаны FK-каскадом).
func (u *ListOperationsUseCase) Execute(ctx context.Context, niID string, p Pagination) ([]operations.Operation, string, error) {
	if err := niResourceID(niID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: niID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
