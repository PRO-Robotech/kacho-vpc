// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/outbox/drainer"
	iamv1 "github.com/PRO-Robotech/kacho-iam/proto/gen/go/kacho/cloud/iam/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
)

// fakeIAMRegisterClient — записывающий fake, реализующий два FGA-proxy RPC
// iamv1.InternalIAMServiceClient, которые использует applier. Остальные методы
// applier'у не нужны и здесь не объявлены.
type fakeIAMRegisterClient struct {
	mu            sync.Mutex
	registerCalls []*iamv1.RegisterResourceRequest
	unregCalls    []*iamv1.UnregisterResourceRequest
	// errSeq отдает ошибку для N-го вызова (по порядку); когда исчерпан — nil (OK).
	// Общий для register/unregister.
	errSeq []error
	calls  int
}

func (f *fakeIAMRegisterClient) nextErr() error {
	f.calls++
	if f.calls-1 < len(f.errSeq) {
		return f.errSeq[f.calls-1]
	}
	return nil
}

func (f *fakeIAMRegisterClient) RegisterResource(ctx context.Context, in *iamv1.RegisterResourceRequest, _ ...grpc.CallOption) (*iamv1.RegisterResourceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.nextErr(); err != nil {
		return nil, err
	}
	f.registerCalls = append(f.registerCalls, in)
	return &iamv1.RegisterResourceResponse{}, nil
}

func (f *fakeIAMRegisterClient) UnregisterResource(ctx context.Context, in *iamv1.UnregisterResourceRequest, _ ...grpc.CallOption) (*iamv1.UnregisterResourceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.nextErr(); err != nil {
		return nil, err
	}
	f.unregCalls = append(f.unregCalls, in)
	return &iamv1.UnregisterResourceResponse{}, nil
}

// applierFor строит тестируемый applier поверх fake IAM-клиента, адаптируя его под
// голый tuple (эти тесты проверяют поведение по идентичности tuple; mirror-feed
// labels/parent покрыт в iam_register_applier_labels_test.go).
func applierFor(c iamRegisterRPC) func(context.Context, string, fgaregister.Tuple) error {
	apply := NewIAMRegisterApplier(c)
	return func(ctx context.Context, eventType string, t fgaregister.Tuple) error {
		return apply(ctx, eventType, fgaregister.Payload{Tuple: t})
	}
}

// register-intent применен → RegisterResource вызван один раз.
func TestIAMRegisterApplier_Register_OK(t *testing.T) {
	f := &fakeIAMRegisterClient{}
	apply := applierFor(f)
	err := apply(context.Background(), fgaregister.EventRegister,
		fgaregister.ProjectHierarchy("proj-x", "vpc_network", "net-1"))
	require.NoError(t, err)
	require.Len(t, f.registerCalls, 1)
	assert.Equal(t, "project:proj-x", f.registerCalls[0].GetSubjectId())
	assert.Equal(t, "project", f.registerCalls[0].GetRelation())
	assert.Equal(t, "vpc_network:net-1", f.registerCalls[0].GetObject())
}

// unregister-intent применен → UnregisterResource вызван один раз.
func TestIAMRegisterApplier_Unregister_OK(t *testing.T) {
	f := &fakeIAMRegisterClient{}
	apply := applierFor(f)
	err := apply(context.Background(), fgaregister.EventUnregister,
		fgaregister.ProjectHierarchy("proj-x", "vpc_security_group", "sgr-1"))
	require.NoError(t, err)
	require.Len(t, f.unregCalls, 1)
	assert.Equal(t, "vpc_security_group:sgr-1", f.unregCalls[0].GetObject())
}

// IAM Unavailable → transient-ошибка пробрасывается raw (drainer ретраит; intent
// остается durable). НЕ ErrPermanent.
func TestIAMRegisterApplier_Unavailable_Transient(t *testing.T) {
	f := &fakeIAMRegisterClient{errSeq: []error{status.Error(codes.Unavailable, "iam down")}}
	apply := applierFor(f)
	err := apply(context.Background(), fgaregister.EventRegister,
		fgaregister.ProjectHierarchy("proj-x", "vpc_network", "net-1"))
	require.Error(t, err)
	assert.False(t, errors.Is(err, drainer.ErrPermanent), "Unavailable must be transient (retry), not poison")
	assert.Contains(t, err.Error(), "Unavailable")
}

// идемпотентный re-apply — контракт IAM: повтор того же tuple → OK (не
// AlreadyExists). Два apply → без ошибки, два RPC OK (IAM дедупит на сервере).
func TestIAMRegisterApplier_Idempotent_Reapply(t *testing.T) {
	f := &fakeIAMRegisterClient{}
	apply := applierFor(f)
	tup := fgaregister.ProjectHierarchy("proj-x", "vpc_network", "net-1")
	require.NoError(t, apply(context.Background(), fgaregister.EventRegister, tup))
	require.NoError(t, apply(context.Background(), fgaregister.EventRegister, tup))
	assert.Len(t, f.registerCalls, 2, "repeat returns OK (idempotent contract)")
}

// permanent-ошибка (InvalidArgument) → ErrPermanent → drainer poison'ит, без
// бесконечных ретраев.
func TestIAMRegisterApplier_InvalidArgument_Permanent(t *testing.T) {
	f := &fakeIAMRegisterClient{errSeq: []error{status.Error(codes.InvalidArgument, "malformed tuple")}}
	apply := applierFor(f)
	err := apply(context.Background(), fgaregister.EventRegister,
		fgaregister.ProjectHierarchy("proj-x", "vpc_network", "net-1"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, drainer.ErrPermanent), "InvalidArgument must be poison (no retry)")
}

// PermissionDenied «grant еще не засеян» (tuple fga_writer@iam_fgaproxy:system у SA
// пока не провижен) должен классифицироваться как TRANSIENT — drainer ретраит,
// intent остается durable (sent_at NULL), и owner-tuple доставляется, как только
// grant осядет. НЕ должно быть ErrPermanent (poison) — иначе потеряли бы tuple
// из-за временного порядка provisioning'а. Poison'ит только InvalidArgument.
func TestIAMRegisterApplier_PermissionDenied_Transient(t *testing.T) {
	f := &fakeIAMRegisterClient{errSeq: []error{status.Error(codes.PermissionDenied, "no fga_writer relation")}}
	apply := applierFor(f)
	err := apply(context.Background(), fgaregister.EventRegister,
		fgaregister.ProjectHierarchy("proj-x", "vpc_network", "net-1"))
	require.Error(t, err)
	assert.False(t, errors.Is(err, drainer.ErrPermanent),
		"PermissionDenied (grant not yet seeded) must be transient (retry), not poison")
	assert.Contains(t, err.Error(), "PermissionDenied")
}

// Неизвестный event_type → permanent (баг вызывающего).
func TestIAMRegisterApplier_UnknownEvent_Permanent(t *testing.T) {
	f := &fakeIAMRegisterClient{}
	apply := applierFor(f)
	err := apply(context.Background(), "fga.bogus",
		fgaregister.ProjectHierarchy("proj-x", "vpc_network", "net-1"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, drainer.ErrPermanent))
}

// DecodeFGARegisterPayload round-trip'ит JSON-payload строки; malformed → poison.
func TestDecodeFGARegisterPayload(t *testing.T) {
	good := []byte(`{"subject_id":"project:proj-x","relation":"project","object":"vpc_network:net-1"}`)
	p, err := DecodeFGARegisterPayload(good)
	require.NoError(t, err)
	assert.Equal(t, "project:proj-x", p.SubjectID)
	assert.Equal(t, "vpc_network:net-1", p.Object)

	_, err = DecodeFGARegisterPayload([]byte(`{not json`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, drainer.ErrPermanent))

	_, err = DecodeFGARegisterPayload([]byte(`{"subject_id":"","relation":"","object":""}`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, drainer.ErrPermanent), "incomplete tuple → poison")
}
