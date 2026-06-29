// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
)

// Проверяет, что register-applier прокидывает labels/parent_project_id/source_version
// из outbox-payload в kacho-iam RegisterResourceRequest, чтобы IAM материализовал
// resource_mirror для селектора.

// register-payload: labels+parent+source_version прокинуты в IAM.
func Test_T3_01_RegisterApplier_ForwardsLabelsAndParent(t *testing.T) {
	f := &fakeIAMRegisterClient{}
	apply := NewIAMRegisterApplier(f)

	now := time.Now().UTC().Truncate(time.Second)
	p := fgaregister.Payload{
		Tuple:           fgaregister.ProjectHierarchy("prj-P", "vpc_subnet", "sub-1"),
		Labels:          map[string]string{"env": "prod", "team": "core"},
		ParentProjectID: "prj-P",
		SourceVersion:   now,
	}
	require.NoError(t, apply(context.Background(), fgaregister.EventRegister, p))

	require.Len(t, f.registerCalls, 1)
	req := f.registerCalls[0]
	assert.Equal(t, "vpc_subnet:sub-1", req.GetObject())
	assert.Equal(t, map[string]string{"env": "prod", "team": "core"}, req.GetLabels())
	assert.Equal(t, "prj-P", req.GetParentProjectId())
	require.NotNil(t, req.GetSourceVersion(), "source_version forwarded")
	assert.True(t, req.GetSourceVersion().AsTime().Equal(now))
}

// payload с нулевым source_version прокидывает nil — IAM трактует nil как
// -infinity (никогда не выигрывает монотонное сравнение).
func Test_T3_01_RegisterApplier_ZeroSourceVersion_ForwardsNil(t *testing.T) {
	f := &fakeIAMRegisterClient{}
	apply := NewIAMRegisterApplier(f)

	p := fgaregister.Payload{Tuple: fgaregister.ProjectHierarchy("p", "vpc_network", "net-1")}
	require.NoError(t, apply(context.Background(), fgaregister.EventRegister, p))

	require.Len(t, f.registerCalls, 1)
	assert.Nil(t, f.registerCalls[0].GetSourceVersion(), "zero source_version → nil")
	assert.Empty(t, f.registerCalls[0].GetLabels())
}

// decoder превращает outbox-payload в Payload (labels + parent).
func Test_T3_01_DecodeFGARegisterPayload(t *testing.T) {
	raw := []byte(`{"subject_id":"project:prj-P","relation":"project","object":"vpc_subnet:sub-1","labels":{"env":"prod"},"parent_project_id":"prj-P"}`)
	got, err := DecodeFGARegisterPayload(raw)
	require.NoError(t, err)
	assert.Equal(t, "vpc_subnet:sub-1", got.Tuple.Object)
	assert.Equal(t, map[string]string{"env": "prod"}, got.Labels)
	assert.Equal(t, "prj-P", got.ParentProjectID)
}
