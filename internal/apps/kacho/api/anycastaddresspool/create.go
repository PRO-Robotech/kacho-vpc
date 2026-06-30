// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// CreateInput — параметры создания пула. CIDRBlocks пуст → platform-assigned.
// NetworkID непуст → пул атомарно аттачится к сети в той же операции (one-step
// «создать пул для этой сети»); сеть обязана быть того же проекта.
type CreateInput struct {
	ProjectID   string
	Name        string
	Description string
	Labels      map[string]string
	Scope       domain.AnycastScope
	IPVersion   domain.IpVersion
	CIDRBlocks  []string
	NetworkID   string
}

// CreateAnycastAddressPoolUseCase — async Create через Operation Worker.
// Sync-валидация (project_id, scope, ip_version, cidr_blocks) — ДО Operation
// (fast-fail gRPC-status). Async-часть (doCreate): project-exists (fail-closed) +
// platform-assign пустого cidr_blocks + Insert (с материализацией child-блоков) +
// outbox + FGA owner-tuple — атомарно в одной writer-TX.
type CreateAnycastAddressPoolUseCase struct {
	repo          Repo
	projectClient ProjectClient
	opsRepo       operations.Repo
	registrar     fgaregister.Registrar
	networkReader NetworkReader
}

// NewCreateAnycastAddressPoolUseCase собирает use-case.
func NewCreateAnycastAddressPoolUseCase(r Repo, projectClient ProjectClient, opsRepo operations.Repo) *CreateAnycastAddressPoolUseCase {
	return &CreateAnycastAddressPoolUseCase{repo: r, projectClient: projectClient, opsRepo: opsRepo}
}

// WithRegistrar подключает синхронный owner-tuple registrar (после commit
// owner-tuple синхронно регистрируется в kacho-iam). Nil → только async drainer.
func (u *CreateAnycastAddressPoolUseCase) WithRegistrar(r fgaregister.Registrar) *CreateAnycastAddressPoolUseCase {
	u.registrar = r
	return u
}

// WithNetworkReader подключает NetworkReader для one-step create+attach
// (валидация существования и same-project сети при заданном NetworkID). Nil →
// NetworkID запрещён (InvalidArgument), пул создаётся только standalone.
func (u *CreateAnycastAddressPoolUseCase) WithNetworkReader(nr NetworkReader) *CreateAnycastAddressPoolUseCase {
	u.networkReader = nr
	return u
}

// Execute — sync-валидация + create Operation + запуск worker'а.
func (u *CreateAnycastAddressPoolUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	if in.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	// scope: фаза 1 — только INTERNAL. EXTERNAL → InvalidArgument; UNSPECIFIED
	// трактуется как INTERNAL (дефолт).
	if in.Scope == domain.AnycastScopeExternal {
		return nil, status.Error(codes.InvalidArgument, "Illegal argument scope: only INTERNAL is supported")
	}
	in.Scope = domain.AnycastScopeInternal
	// ip_version обязателен и должен быть IPV4 или IPV6.
	if in.IPVersion != domain.IpVersionIPv4 && in.IPVersion != domain.IpVersionIPv6 {
		return nil, status.Error(codes.InvalidArgument, "Illegal argument ip_version: must be IPV4 or IPV6")
	}
	// BYO cidr_blocks валидируются sync; пустой набор → platform-assign в worker'е.
	if len(in.CIDRBlocks) > 0 {
		if err := validateCIDRBlocks(in.CIDRBlocks, in.IPVersion); err != nil {
			return nil, err
		}
	}
	// network_id (optional one-step attach) — валидируем формат, существование и
	// same-project синхронно (fast-fail), как в AttachNetwork. Сам attach — в
	// doCreate, атомарно в writer-TX с Insert пула.
	if in.NetworkID != "" {
		if u.networkReader == nil {
			return nil, status.Error(codes.InvalidArgument, "network_id is not supported")
		}
		if err := corevalidate.ResourceID("network", ids.PrefixNetwork, in.NetworkID); err != nil {
			return nil, err
		}
		net, nerr := u.networkReader.Get(ctx, in.NetworkID)
		if nerr != nil {
			if errors.Is(nerr, repo.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Network %s not found", in.NetworkID)
			}
			return nil, serviceerr.MapRepoErr(nerr)
		}
		if net.ProjectID != in.ProjectID {
			return nil, status.Error(codes.InvalidArgument,
				"Illegal argument networkId: network and anycast address pool must belong to the same project")
		}
	}
	// Self-validating domain: name/description/labels.
	probe := domain.AnycastAddressPool{
		Name:        domain.RcNameVPC(in.Name),
		Description: domain.RcDescription(in.Description),
		Labels:      domain.LabelsFromMap(in.Labels),
	}
	if err := serviceerr.FromValidation(probe.Validate()); err != nil {
		return nil, err
	}

	poolID := ids.NewID(ids.PrefixAnycastPool)
	op, err := operations.NewFromContext(
		ctx,
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create anycast address pool %s", in.Name),
		&vpcv1.CreateAnycastAddressPoolMetadata{AnycastAddressPoolId: poolID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, poolID, in)
	})
	return &op, nil
}

// doCreate — async-часть Create. project-exists (fail-closed Unavailable) +
// platform-assign + Insert + outbox + FGA owner-tuple в одной writer-TX.
func (u *CreateAnycastAddressPoolUseCase) doCreate(ctx context.Context, poolID string, in CreateInput) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, in.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "project check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Project %s not found", in.ProjectID)
	}

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer w.Abort()

	blocks := in.CIDRBlocks
	if len(blocks) == 0 {
		allocated, aerr := w.AnycastAddressPools().AllocatedBlocks(ctx)
		if aerr != nil {
			return nil, serviceerr.MapRepoErr(aerr)
		}
		block, ok := assignBlock(in.IPVersion, allocated)
		if !ok {
			return nil, status.Error(codes.FailedPrecondition, "could not assign anycast address pool CIDR")
		}
		blocks = []string{block}
	}

	pool := &domain.AnycastAddressPool{
		ID:          poolID,
		ProjectID:   in.ProjectID,
		Name:        domain.RcNameVPC(in.Name),
		Description: domain.RcDescription(in.Description),
		Labels:      domain.LabelsFromMap(in.Labels),
		Scope:       in.Scope,
		IPVersion:   in.IPVersion,
		CIDRBlocks:  blocks,
		IsDefault:   false,
		Status:      domain.AnycastPoolStatusActive,
	}
	created, err := w.AnycastAddressPools().Insert(ctx, pool)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "AnycastAddressPool", created.ID, "CREATED",
		helpers.DomainToMap(created.AnycastAddressPool)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	items := []fgaregister.Item{
		fgaregister.ProjectHierarchyItem(created.ProjectID, "vpc_anycast_address_pool", created.ID,
			domain.LabelsToMap(created.Labels)),
	}
	if err := w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(items...)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: fga register intent: %v", repo.ErrInternal, err))
	}
	// One-step attach: пул аттачится к сети в той же writer-TX (атомарно с Insert).
	// Existence/same-project проверены sync в Execute; здесь FK + claim-EXCLUDE —
	// финальный DB-гард (race с удалением сети / claim-overlap → rollback всего Create).
	if in.NetworkID != "" {
		if aerr := w.AnycastAddressPools().AttachNetwork(ctx, created.ID, in.NetworkID, blocks); aerr != nil {
			return nil, serviceerr.MapRepoErr(aerr)
		}
		if oerr := w.Outbox().Emit(ctx, "AnycastAddressPool", created.ID, "NETWORK_ATTACHED",
			map[string]any{"pool_id": created.ID, "network_id": in.NetworkID}); oerr != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
	}
	if err := w.Commit(); err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if u.registrar != nil {
		if err := u.registrar.Register(ctx, items); err != nil {
			return nil, err
		}
	}
	return anypb.New(toProto(created))
}
