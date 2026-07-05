// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// fakeProbe — in-memory ResourceExistenceProbe для unit-тестов existence-hiding.
type fakeProbe struct {
	exists map[string]bool // "<type>:<id>" -> существует ли строка в БД vpc
	err    error
}

func (f *fakeProbe) ObjectExists(_ context.Context, objectType, objectID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.exists[objectType+":"+objectID], nil
}

// denyResp — отказ iam с reason "lacks relation" (как для garbage vpc id): НЕ
// содержит "no path", поэтому без existence-probe попал бы в plain-deny → 403.
func denyResp(object string) *iamv1.CheckResponse {
	return &iamv1.CheckResponse{
		Allowed: false,
		Reason:  "subject user:usr_b lacks relation \"v_get\" on " + object + "; no direct relations granted",
	}
}

func newProbeClientCtx(t *testing.T, resp *iamv1.CheckResponse, probe ResourceExistenceProbe) (*IAMCheckClient, context.Context) {
	t.Helper()
	fake := &fakeInternalIAM{resp: resp}
	conn := startFakeInternalIAM(t, fake)
	client := NewIAMCheckClientWithProbe(conn, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return client, ctx
}

// Decision 1 GWT-1.1/1.2/1.3: object-scoped deny на ОТСУТСТВУЮЩИЙ vpc-объект →
// existence-probe говорит "нет" → возвращаем ErrNoPath (interceptor пропускает к
// handler'у, который отдаст verbatim NotFound 404), а не plain-deny 403.
func TestIAMCheckClient_ExistenceHiding_AbsentObject_PassesThrough(t *testing.T) {
	const object = "vpc_network:enpabsent99999999999"
	client, ctx := newProbeClientCtx(t, denyResp(object), &fakeProbe{exists: map[string]bool{}})

	allowed, err := client.Check(ctx, "user:usr_b", "v_get", object)
	assert.False(t, allowed)
	require.ErrorIs(t, err, authz.ErrNoPath,
		"absent object-scoped deny must map to ErrNoPath (passthrough → handler 404)")
}

// Decision 1 GWT-1.4: object-scoped deny на СУЩЕСТВУЮЩИЙ объект (cross-owner) →
// existence-probe говорит "есть" → ErrHideExistence (interceptor блокирует handler
// и отдает NotFound 404, скрывая существование). «Есть-но-не-твой» неотличимо от
// «нет такого»; handler недостижим → no tamper, no leak.
func TestIAMCheckClient_ExistenceHiding_PresentObject_HidesExistence(t *testing.T) {
	const object = "vpc_network:enppresent9999999999"
	client, ctx := newProbeClientCtx(t, denyResp(object),
		&fakeProbe{exists: map[string]bool{object: true}})

	allowed, err := client.Check(ctx, "user:usr_b", "v_delete", object)
	require.ErrorIs(t, err, authz.ErrHideExistence,
		"present cross-owner object → ErrHideExistence (deny→NotFound, no leak)")
	assert.False(t, allowed)
}

// Decision 1 GWT-1.5: ошибка existence-probe (БД недоступна) → fail-closed: deny
// остается, НЕ passthrough (не светим/не трогаем при неопределенности).
func TestIAMCheckClient_ExistenceHiding_ProbeError_KeepsDeny(t *testing.T) {
	const object = "vpc_subnet:snpx9999999999999999"
	client, ctx := newProbeClientCtx(t, denyResp(object),
		&fakeProbe{err: errors.New("db unavailable")})

	allowed, err := client.Check(ctx, "user:usr_b", "v_get", object)
	require.NoError(t, err)
	assert.False(t, allowed, "probe error → keep deny (fail-closed, no passthrough)")
}

// Decision 1 GWT-1.6: collection-scoped объект (project:) НЕ object-scoped → не
// probe-ится → plain deny (PermissionDenied), existence-hiding не применяется.
func TestIAMCheckClient_ExistenceHiding_NonVPCObject_KeepsDeny(t *testing.T) {
	const object = "project:prj_acme9999999999999"
	probe := &fakeProbe{exists: map[string]bool{}} // даже absent — не должно влиять
	client, ctx := newProbeClientCtx(t, denyResp(object), probe)

	allowed, err := client.Check(ctx, "user:usr_b", "editor", object)
	require.NoError(t, err)
	assert.False(t, allowed, "project-scoped deny must stay PermissionDenied")
}

// Decision 1 GWT-1.5 (infra-fail): iam transport-ошибка → возвращаем ошибку
// (interceptor → fail-closed PermissionDenied), НИКОГДА не ErrNoPath/404.
func TestIAMCheckClient_ExistenceHiding_IAMTransportError_FailClosed(t *testing.T) {
	fake := &fakeInternalIAM{err: errors.New("iam down")}
	conn := startFakeInternalIAM(t, fake)
	probe := &fakeProbe{exists: map[string]bool{}} // absent — но это не должно дать passthrough
	client := NewIAMCheckClientWithProbe(conn, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.Check(ctx, "user:usr_b", "v_get", "vpc_network:enpabsent99999999999")
	require.Error(t, err)
	assert.NotErrorIs(t, err, authz.ErrNoPath, "transport error must NOT be existence-hidden")
}

// Без probe (nil) поведение прежнее: "no path"-reason → ErrNoPath, остальной
// deny → plain deny.
func TestIAMCheckClient_NilProbe_BackCompat(t *testing.T) {
	client, ctx := newProbeClientCtx(t,
		&iamv1.CheckResponse{Allowed: false, Reason: "no path: unscoped resource"}, nil)
	allowed, err := client.Check(ctx, "user:usr_b", "viewer", "project:prj_x99999999999999")
	assert.False(t, allowed)
	require.ErrorIs(t, err, authz.ErrNoPath)
}
