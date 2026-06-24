// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// vpc публикует labels+parent в FGA register-intent, чтобы kacho-iam
// материализовал resource_mirror для label-селектора. Эти use-case-тесты
// фиксируют наблюдаемую emit-политику:
//   - Create Subnet с labels → register intent несет labels + parent_project_id.
//   - Update Subnet labels (labels в update_mask) → НОВЫЙ register intent с
//     обновленными labels.
//   - Update Subnet БЕЗ labels в mask → НОВОГО register intent нет (no-op).
//
// register-intent эмитится в той же writer-TX, что и DML (kachomock сбрасывает
// localFGARegister на Commit) — атомарность проверяется integration writer-тестом;
// здесь проверяем labels/parent payload + триггер по Update-маске.

// subnetRegisters возвращает register-события для указанного subnet id.
func subnetRegisters(kr *kachomock.Repository, subnetID string) []kachomock.FGARegisterEvent {
	var out []kachomock.FGARegisterEvent
	for _, e := range kr.FGARegisterEvents() {
		if e.EventType == "fga.register" && e.Tuple.Object == "vpc_subnet:"+subnetID {
			out = append(out, e)
		}
	}
	return out
}

// createSubnet вызывает CreateSubnet, дожидается операции и возвращает id созданной подсети.
func createSubnet(t *testing.T, h *Handler, or *repomock.OpsRepo, netID, projectID string, labels map[string]string) string {
	t.Helper()
	ctx := context.Background()
	op, err := h.Create(ctx, &vpcv1.CreateSubnetRequest{
		ProjectId:    projectID,
		NetworkId:    netID,
		ZoneId:       testZone,
		Name:         "sub-t3",
		V4CidrBlocks: []string{"10.20.0.0/24"},
		Labels:       labels,
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error, "Create op must succeed")

	resp, err := h.List(ctx, &vpcv1.ListSubnetsRequest{ProjectId: projectID})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Subnets)
	return resp.Subnets[0].Id
}

// Create Subnet с labels → register intent несет labels + parent.
func Test_T3_01_CreateSubnet_RegisterIntentCarriesLabelsAndParent(t *testing.T) {
	h, or, kr, netID := minimalHandler(t, true)

	subID := createSubnet(t, h, or, netID, "f1",
		map[string]string{"env": "prod", "team": "core"})

	regs := subnetRegisters(kr, subID)
	require.Len(t, regs, 1, "exactly one register intent on Create")
	assert.Equal(t, map[string]string{"env": "prod", "team": "core"}, regs[0].Labels,
		"register intent carries the subnet labels (T3-01 mirror feed)")
	assert.Equal(t, "f1", regs[0].ParentProjectID,
		"parent_project_id = subnet project_id")
}

// Create Subnet БЕЗ labels → labels пустые, но parent все равно проставлен.
func Test_T3_01_CreateSubnet_NoLabels_EmptyLabelsParentSet(t *testing.T) {
	h, or, kr, netID := minimalHandler(t, true)

	subID := createSubnet(t, h, or, netID, "f1", nil)

	regs := subnetRegisters(kr, subID)
	require.Len(t, regs, 1)
	assert.Empty(t, regs[0].Labels, "no labels on Create → empty labels in intent")
	assert.Equal(t, "f1", regs[0].ParentProjectID)
}

// Update Subnet labels (labels в update_mask) → НОВЫЙ register intent с
// обновленными labels.
func Test_T3_01_UpdateSubnetLabels_EmitsNewRegisterIntent(t *testing.T) {
	h, or, kr, netID := minimalHandler(t, true)
	ctx := context.Background()

	subID := createSubnet(t, h, or, netID, "f1", map[string]string{"env": "prod"})

	op, err := h.Update(ctx, &vpcv1.UpdateSubnetRequest{
		SubnetId:   subID,
		Labels:     map[string]string{"env": "dev", "tier": "gold"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	regs := subnetRegisters(kr, subID)
	require.Len(t, regs, 2, "Create + Update(labels) → two register intents")
	assert.Equal(t, map[string]string{"env": "dev", "tier": "gold"}, regs[1].Labels,
		"the Update register intent carries the refreshed labels")
	assert.Equal(t, "f1", regs[1].ParentProjectID)
}

// Update Subnet БЕЗ labels в mask → НОВОГО register intent нет.
func Test_T3_01_UpdateSubnetNonLabels_NoNewRegisterIntent(t *testing.T) {
	h, or, kr, netID := minimalHandler(t, true)
	ctx := context.Background()

	subID := createSubnet(t, h, or, netID, "f1", map[string]string{"env": "prod"})

	op, err := h.Update(ctx, &vpcv1.UpdateSubnetRequest{
		SubnetId:   subID,
		Name:       "renamed",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	regs := subnetRegisters(kr, subID)
	require.Len(t, regs, 1, "non-labels Update → no new register intent (β-04b no-op)")
}
