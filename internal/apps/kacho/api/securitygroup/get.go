// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// enforceGetVisible применяет per-object no-leak: если filter != nil, subject
// не пуст и SG id вне accessible-set (того же FGA grant-set, что и List —
// read==enforce) → NotFound с тем же текстом, что и несуществующий SG (без
// existence-leak; см. helpers.WrapSGErr — "Security group SecurityGroup.Id(value=%s)
// not found"). FGA-ошибка → fail-closed (Unavailable).
func enforceGetVisible(ctx context.Context, filter ListFilter, subjectID, id string) error {
	var port authzfilter.UseCasePort
	if filter != nil {
		port = filter
	}
	visible, err := authzfilter.EnforceVisible(ctx, port, subjectID,
		authzfilter.ResourceTypeSecurityGroup, authzfilter.ActionSecurityGroupList, id)
	if err != nil {
		return err
	}
	if !visible {
		return serviceerr.MapRepoErr(fmt.Errorf("%w: Security group SecurityGroup.Id(value=%s) not found", serviceerr.ErrNotFound, id))
	}
	return nil
}

// GetSecurityGroupUseCase — простой read; единственная «логика» — id-валидация,
// перевод repo-sentinel в gRPC status и per-object no-leak enforce. Use-case
// можно было бы опустить, но handler-у удобнее единый шов через use-case'ы.
// Открывает CQRS Reader, читает, закрывает; read-only TX — параллельный writer
// не блокируется.
//
// Per-object no-leak: если filter != nil и subject не пуст — после repo.Get
// проверяем, что id входит в accessible-set того же FGA grant-set, что и List
// (read==enforce). filter == nil / subject == "" → enforce делает per-RPC
// interceptor (dev / system-principal).
type GetSecurityGroupUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewGetSecurityGroupUseCase создает GetSecurityGroupUseCase. filter может быть
// nil (list-filter disabled / dev) → no-leak enforce пропускается.
func NewGetSecurityGroupUseCase(r Repo, filter ListFilter) *GetSecurityGroupUseCase {
	return &GetSecurityGroupUseCase{repo: r, filter: filter}
}

// Execute возвращает repo-entity SG. NotFound → mapRepoErr → gRPC NotFound.
// Per-object no-leak: subject без гранта на SG → NotFound.
func (u *GetSecurityGroupUseCase) Execute(ctx context.Context, subjectID, id string) (*kacho.SecurityGroupRecord, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	sg, err := rd.SecurityGroups().Get(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := enforceGetVisible(ctx, u.filter, subjectID, id); err != nil {
		return nil, err
	}
	return sg, nil
}
