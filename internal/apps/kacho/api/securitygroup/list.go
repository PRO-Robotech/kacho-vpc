// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListSecurityGroupsUseCase — список SG с пагинацией. project_id обязателен
// (закрывает cross-project enumeration). Читает через CQRS Reader (read-only TX).
//
// Per-object filtered List через FGA ListObjects (relation viewer; read==enforce).
// filter==nil / subject=="" → passthrough.
type ListSecurityGroupsUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListSecurityGroupsUseCase создает ListSecurityGroupsUseCase. filter может
// быть nil (list-filter disabled / dev) → unfiltered passthrough.
func NewListSecurityGroupsUseCase(r Repo, filter ListFilter) *ListSecurityGroupsUseCase {
	return &ListSecurityGroupsUseCase{repo: r, filter: filter}
}

// Execute — project_id required + per-object FGA-filter. Пагинация ПОСЛЕ фильтра
// (repo.ListByIDs); bypass → repo.List; empty grant → пустой (no-leak);
// iam недоступен → fail-closed Unavailable.
func (u *ListSecurityGroupsUseCase) Execute(ctx context.Context, subjectID string, f SecurityGroupFilter, p Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
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
		return rd.SecurityGroups().List(ctx, f, p)
	}
	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeSecurityGroup, authzfilter.ActionSecurityGroupList)
	if ferr != nil {
		return nil, "", ferr
	}
	if bypass {
		return rd.SecurityGroups().List(ctx, f, p)
	}
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	return rd.SecurityGroups().ListByIDs(ctx, f, allowedIDs, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному SG.
//
// Семантика: с repo.Get-precondition (для SG ListOperations предполагает, что SG
// еще жив; если удален — возвращается sync NotFound через precondition Get).
type ListOperationsUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewListOperationsUseCase создает ListOperationsUseCase.
func NewListOperationsUseCase(r Repo, opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — id-валидация + existence-check + список операций.
func (u *ListOperationsUseCase) Execute(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, "", err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	if _, gerr := rd.SecurityGroups().Get(ctx, id); gerr != nil {
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
