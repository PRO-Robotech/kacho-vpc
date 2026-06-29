// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package networkinternal — internal-операции над Network, которые не выражаются
// через публичный Update (управление computed-полями сети).
//
// Доступен через gRPC kacho.cloud.vpc.v1.InternalNetworkService. Это не-resource
// service: ресурс-CRUD сети живет в internal/apps/kacho/api/network/.
package networkinternal

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// NetworkRepo — узкий port-интерфейс над repo.NetworkRepo: только методы,
// нужные для SetDefaultSecurityGroupId.
//
// SetDefaultSGID — CAS-проставление default_security_group_id (узкий
// column-update + outbox-emit в одной writer-TX). Узкий update не затирает
// конкурентный rename сети и закрывает TOCTOU между read и записью.
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
	SetDefaultSGID(ctx context.Context, networkID, sgID string) (*kachorepo.NetworkRecord, error)
}

// SecurityGroupRepo — узкий port-интерфейс над repo.SecurityGroupRepo: только
// Get (для FK-валидации SG→Network).
type SecurityGroupRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.SecurityGroupRecord, error)
}

// Service — internal-only операции над Network (computed-поля).
type Service struct {
	repo NetworkRepo
	sgs  SecurityGroupRepo
}

// NewService создает Service.
func NewService(repo NetworkRepo, sgs SecurityGroupRepo) *Service {
	return &Service{repo: repo, sgs: sgs}
}

// GetNetwork возвращает запись сети с инфра-полем VRFID (data-plane tenancy).
// Используется InternalNetworkService.GetNetwork — НЕ публичный read.
func (s *Service) GetNetwork(ctx context.Context, id string) (*kachorepo.NetworkRecord, error) {
	return s.repo.Get(ctx, id)
}

// SetDefaultSecurityGroupId — выставляет computed-поле
// Network.default_security_group_id. Public Update API не принимает это
// поле в UpdateMask (immutable / output-only по convention).
//
// Idempotent: повторный вызов с тем же sg_id — no-op.
// FailedPrecondition если уже задан другой sg_id (защита от случайного
// перезаписывания).
func (s *Service) SetDefaultSecurityGroupId(ctx context.Context, networkID, sgID string) error {
	n, err := s.repo.Get(ctx, networkID)
	if err != nil {
		return err
	}
	if n.DefaultSecurityGroupID == sgID {
		return nil // idempotent
	}
	if n.DefaultSecurityGroupID != "" {
		return status.Errorf(codes.FailedPrecondition,
			"network %s already has default_security_group_id=%s; refusing to overwrite with %s",
			networkID, n.DefaultSecurityGroupID, sgID)
	}
	// Validate FK: sg должна существовать и принадлежать этой network.
	sg, err := s.sgs.Get(ctx, sgID)
	if err != nil {
		return err
	}
	if sg.NetworkID != networkID {
		return status.Errorf(codes.InvalidArgument,
			"security group %s belongs to network %s, not %s",
			sgID, sg.NetworkID, networkID)
	}
	// Атомарный CAS: проставляем только если поле все еще пусто/равно sgID.
	// Узкий column-update не затирает name/description/labels конкурентного
	// Network.Update, а CAS закрывает TOCTOU между Get выше и записью.
	if _, err = s.repo.SetDefaultSGID(ctx, networkID, sgID); err != nil {
		if errors.Is(err, repo.ErrFailedPrecondition) {
			// Конкурентный writer выставил другой SG между нашим Get и CAS.
			// Перечитываем для точного текста (тот же, что precedence-проверка).
			if cur, gerr := s.repo.Get(ctx, networkID); gerr == nil && cur.DefaultSecurityGroupID != "" && cur.DefaultSecurityGroupID != sgID {
				return status.Errorf(codes.FailedPrecondition,
					"network %s already has default_security_group_id=%s; refusing to overwrite with %s",
					networkID, cur.DefaultSecurityGroupID, sgID)
			}
		}
		return err
	}
	return nil
}
