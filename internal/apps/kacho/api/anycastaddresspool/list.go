// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

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

// ListAnycastAddressPoolsUseCase — список пулов с пагинацией. project_id
// обязателен (закрывает cross-project enumeration; is_default-пул живёт в
// платформенном project и тенанту не виден). Per-object FGA-filter — read==enforce.
type ListAnycastAddressPoolsUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListAnycastAddressPoolsUseCase создаёт use-case. filter nil → unfiltered
// passthrough.
func NewListAnycastAddressPoolsUseCase(r Repo, filter ListFilter) *ListAnycastAddressPoolsUseCase {
	return &ListAnycastAddressPoolsUseCase{repo: r, filter: filter}
}

// Execute — project_id required + per-object FGA-filter. bypass → repo.List;
// empty grant → пустой (no-leak); anon при включённом фильтре → fail-closed пустой;
// iam недоступен → fail-closed Unavailable.
func (u *ListAnycastAddressPoolsUseCase) Execute(ctx context.Context, subjectID string, f AnycastAddressPoolFilter, p Pagination) ([]*kachorepo.AnycastAddressPoolRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	if subjectID == "" && u.filter != nil {
		return nil, "", nil
	}
	if u.filter == nil || subjectID == authzfilter.SystemSubject {
		return rd.AnycastAddressPools().List(ctx, f, p)
	}
	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeAnycastAddressPool, authzfilter.ActionAnycastAddressPoolList)
	if ferr != nil {
		return nil, "", ferr
	}
	if bypass {
		return rd.AnycastAddressPools().List(ctx, f, p)
	}
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	return rd.AnycastAddressPools().ListByIDs(ctx, f, allowedIDs, p)
}

// ListOperationsUseCase — операции конкретного пула (с existence-precondition).
type ListOperationsUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт use-case.
func NewListOperationsUseCase(r Repo, opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — id-валидация + existence-check + список операций.
func (u *ListOperationsUseCase) Execute(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("anycast address pool", ids.PrefixAnycastPool, id); err != nil {
		return nil, "", err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	if _, gerr := rd.AnycastAddressPools().Get(ctx, id); gerr != nil {
		_ = rd.Close()
		return nil, "", serviceerr.MapRepoErr(gerr)
	}
	_ = rd.Close()
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: id,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
