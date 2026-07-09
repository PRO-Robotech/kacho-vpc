// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addressref"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// fakeAllocator — стаб AddressAllocator: фиксирует, какой allocate-метод позвал
// handler, и какой результат/ошибку вернуть.
type fakeAllocator struct {
	lastCall string
	lastID   string
	result   *domain.AllocateResult
	err      error
}

func (f *fakeAllocator) AllocateInternalIP(_ context.Context, id string) (*domain.AllocateResult, error) {
	f.lastCall, f.lastID = "AllocateInternalIP", id
	return f.result, f.err
}

func (f *fakeAllocator) AllocateInternalIPv6(_ context.Context, id string) (*domain.AllocateResult, error) {
	f.lastCall, f.lastID = "AllocateInternalIPv6", id
	return f.result, f.err
}

func (f *fakeAllocator) AllocateExternalIP(_ context.Context, id string) (*domain.AllocateResult, error) {
	f.lastCall, f.lastID = "AllocateExternalIP", id
	return f.result, f.err
}

func (f *fakeAllocator) AllocateExternalIPv6(_ context.Context, id string) (*domain.AllocateResult, error) {
	f.lastCall, f.lastID = "AllocateExternalIPv6", id
	return f.result, f.err
}

// noopRefs — пустой AddressReferenceManager (allocate-тесты его не используют).
type noopRefs struct{}

func (noopRefs) SetAddressReference(_ context.Context, _ addressref.SetAddressReferenceReq) (*domain.AddressReference, error) {
	return nil, nil
}

func (noopRefs) MarkAddressEphemeralInUse(_ context.Context, _ addressref.SetAddressReferenceReq) (*domain.AddressReference, error) {
	return nil, nil
}

func (noopRefs) ClearAddressReference(_ context.Context, _ string) error { return nil }

func (noopRefs) GetAddressReference(_ context.Context, _ string) (*domain.AddressReference, error) {
	return nil, nil
}

// TestHandler_AllocateExternalIPv6_Dispatch — handler проксирует запрос в
// AddressAllocator.AllocateExternalIPv6 (не в v4-метод) и переносит IP+pool_id+
// already_allocated в ответ.
func TestHandler_AllocateExternalIPv6_Dispatch(t *testing.T) {
	alloc := &fakeAllocator{result: &domain.AllocateResult{
		IP:               "2001:db8::1",
		PoolID:           "aplv6test12345678901",
		AlreadyAllocated: false,
	}}
	h := NewInternalAddressAllocateHandler(alloc, noopRefs{})

	resp, err := h.AllocateExternalIPv6(context.Background(),
		&vpcv1.AllocateExternalIPRequest{AddressId: "adr_v6"})
	require.NoError(t, err)
	assert.Equal(t, "AllocateExternalIPv6", alloc.lastCall, "должен звать v6-метод, не v4")
	assert.Equal(t, "adr_v6", alloc.lastID)
	assert.Equal(t, "2001:db8::1", resp.GetIp())
	assert.Equal(t, "aplv6test12345678901", resp.GetPoolId())
	assert.False(t, resp.GetAlreadyAllocated())
}

// TestHandler_AllocateExternalIPv6_AlreadyAllocated — идемпотентный ответ
// (already_allocated=true) пробрасывается клиенту.
func TestHandler_AllocateExternalIPv6_AlreadyAllocated(t *testing.T) {
	alloc := &fakeAllocator{result: &domain.AllocateResult{
		IP:               "2001:db8::7",
		PoolID:           "aplv6test12345678901",
		AlreadyAllocated: true,
	}}
	h := NewInternalAddressAllocateHandler(alloc, noopRefs{})

	resp, err := h.AllocateExternalIPv6(context.Background(),
		&vpcv1.AllocateExternalIPRequest{AddressId: "adr_v6"})
	require.NoError(t, err)
	assert.True(t, resp.GetAlreadyAllocated())
	assert.Equal(t, "2001:db8::7", resp.GetIp())
}

// TestHandler_AllocateExternalIPv6_EmptyID — пустой address_id → sync
// InvalidArgument, allocator не вызывается.
func TestHandler_AllocateExternalIPv6_EmptyID(t *testing.T) {
	alloc := &fakeAllocator{}
	h := NewInternalAddressAllocateHandler(alloc, noopRefs{})

	_, err := h.AllocateExternalIPv6(context.Background(),
		&vpcv1.AllocateExternalIPRequest{AddressId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Empty(t, alloc.lastCall, "allocator не должен вызываться при пустом id")
}

// TestHandler_AllocateExternalIPv6_NotFound — ErrNotFound из use-case'а
// маппится в gRPC NotFound (mapAllocErr).
func TestHandler_AllocateExternalIPv6_NotFound(t *testing.T) {
	alloc := &fakeAllocator{err: repo.ErrNotFound}
	h := NewInternalAddressAllocateHandler(alloc, noopRefs{})

	_, err := h.AllocateExternalIPv6(context.Background(),
		&vpcv1.AllocateExternalIPRequest{AddressId: "adr_missing"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestHandler_AllocateInternalIP_NotFound_MessageTone — не-fabricated regression:
// use-case возвращает repo.ErrNotFound В ОБЁРНУТОМ виде, ровно как его отдаёт
// GetForUpdate → helpers.WrapPgErr (`%w: Address <id> not found`). mapAllocErr
// обязан снять внутренний sentinel-префикс "not found: " и отдать клиенту
// (:9091 IPAM edge — kacho-compute/kacho-nlb) канонический message-tone
// "Address <id> not found" — parity с public NetworkService.Get и с sibling
// reference-методами (serviceerr.MapRepoErr). Без фикса на wire уходит
// "not found: Address adr_missing not found" (утечка формы repo-обёртки).
func TestHandler_AllocateInternalIP_NotFound_MessageTone(t *testing.T) {
	wrapped := fmt.Errorf("%w: Address %s not found", repo.ErrNotFound, "adr_missing")
	alloc := &fakeAllocator{err: wrapped}
	h := NewInternalAddressAllocateHandler(alloc, noopRefs{})

	_, err := h.AllocateInternalIP(context.Background(),
		&vpcv1.AllocateInternalIPRequest{AddressId: "adr_missing"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Equal(t, "Address adr_missing not found", st.Message(),
		"клиент должен видеть канонический tone без внутреннего sentinel-префикса")
	assert.NotContains(t, st.Message(), "not found: ",
		"внутренняя repo-обёртка sentinel'а не должна течь на wire")
}
