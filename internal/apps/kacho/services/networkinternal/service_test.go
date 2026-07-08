// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package networkinternal — service_test.go: unit-тесты SetDefaultSecurityGroupId
// на fake-порт'ах (NetworkRepo/SecurityGroupRepo), без Postgres. Фиксируют
// use-case-логику до атомарного CAS: idempotent no-op, precedence-guard и
// cross-network FK-mismatch (SG принадлежит другой сети → InvalidArgument).
package networkinternal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// fakeNetRepo — узкий fake над NetworkRepo. setCalls фиксирует, дошли ли мы до
// CAS-записи (для проверки «FK-mismatch → SetDefaultSGID НЕ вызывается»).
type fakeNetRepo struct {
	rec      *kachorepo.NetworkRecord
	getErr   error
	setCalls int
}

func (f *fakeNetRepo) Get(_ context.Context, _ string) (*kachorepo.NetworkRecord, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.rec, nil
}

func (f *fakeNetRepo) SetDefaultSGID(_ context.Context, _, _ string) (*kachorepo.NetworkRecord, error) {
	f.setCalls++
	return f.rec, nil
}

// fakeSGRepo — узкий fake над SecurityGroupRepo (только Get).
type fakeSGRepo struct {
	rec *kachorepo.SecurityGroupRecord
	err error
}

func (f fakeSGRepo) Get(_ context.Context, _ string) (*kachorepo.SecurityGroupRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rec, nil
}

// TestSetDefaultSecurityGroupId_CrossNetworkMismatch — SG существует, но
// принадлежит ДРУГОЙ сети → InvalidArgument; до CAS-записи не доходим.
func TestSetDefaultSecurityGroupId_CrossNetworkMismatch(t *testing.T) {
	netID := ids.NewID(ids.PrefixNetwork)
	otherNetID := ids.NewID(ids.PrefixNetwork)
	sgID := ids.NewID(ids.PrefixSecurityGroup)

	nr := &fakeNetRepo{rec: &kachorepo.NetworkRecord{
		Network: domain.Network{ID: netID}, // default_security_group_id пуст
	}}
	sr := fakeSGRepo{rec: &kachorepo.SecurityGroupRecord{
		SecurityGroup: domain.SecurityGroup{ID: sgID, NetworkID: otherNetID}, // чужая сеть
	}}
	s := NewService(nr, sr)

	err := s.SetDefaultSecurityGroupId(context.Background(), netID, sgID)

	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "belongs to network "+otherNetID)
	assert.Equal(t, 0, nr.setCalls, "CAS write must not run on FK-mismatch")
}

// TestSetDefaultSecurityGroupId_OK — SG принадлежит той же сети, поле пусто →
// CAS-запись выполняется, ошибки нет.
func TestSetDefaultSecurityGroupId_OK(t *testing.T) {
	netID := ids.NewID(ids.PrefixNetwork)
	sgID := ids.NewID(ids.PrefixSecurityGroup)

	nr := &fakeNetRepo{rec: &kachorepo.NetworkRecord{Network: domain.Network{ID: netID}}}
	sr := fakeSGRepo{rec: &kachorepo.SecurityGroupRecord{
		SecurityGroup: domain.SecurityGroup{ID: sgID, NetworkID: netID},
	}}
	s := NewService(nr, sr)

	err := s.SetDefaultSecurityGroupId(context.Background(), netID, sgID)

	require.NoError(t, err)
	assert.Equal(t, 1, nr.setCalls)
}

// TestSetDefaultSecurityGroupId_Idempotent — тот же sg_id уже проставлен → no-op,
// SG-repo и CAS-запись не трогаем.
func TestSetDefaultSecurityGroupId_Idempotent(t *testing.T) {
	netID := ids.NewID(ids.PrefixNetwork)
	sgID := ids.NewID(ids.PrefixSecurityGroup)

	nr := &fakeNetRepo{rec: &kachorepo.NetworkRecord{
		Network: domain.Network{ID: netID, DefaultSecurityGroupID: sgID},
	}}
	s := NewService(nr, fakeSGRepo{err: assertUnusedSGGet(t)})

	err := s.SetDefaultSecurityGroupId(context.Background(), netID, sgID)

	require.NoError(t, err)
	assert.Equal(t, 0, nr.setCalls, "idempotent no-op must not write")
}

// assertUnusedSGGet возвращает sentinel-ошибку, которая всплыла бы, если бы
// idempotent-путь ошибочно дернул SecurityGroupRepo.Get.
func assertUnusedSGGet(t *testing.T) error {
	t.Helper()
	return grpcstatus.Error(codes.Internal, "SecurityGroupRepo.Get must not be called on idempotent path")
}
