// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package toproto_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	// blank-import регистрирует трансферы Network + time.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Убеждаемся, что dto.Transfer работает для зарегистрированных пар —
// smoke вокруг init()-цепочки.

func TestDTO_TransferNetworkRecord(t *testing.T) {
	at := time.Date(2026, 5, 15, 12, 34, 56, 789_000_000, time.UTC)
	rec := kachorepo.NetworkRecord{
		Network: domain.Network{
			ID:                     "enp1",
			ProjectID:              "project-x",
			Name:                   domain.RcNameVPC("my-net"),
			Description:            domain.RcDescription("desc"),
			Labels:                 domain.LabelsFromMap(map[string]string{"env": "prod"}),
			DefaultSecurityGroupID: "enpsg",
		},
		CreatedAt: at,
	}
	var dst *vpcv1.Network
	require.NoError(t, dto.Transfer(dto.FromTo(rec, &dst)))
	require.NotNil(t, dst)
	assert.Equal(t, "enp1", dst.Id)
	assert.Equal(t, "project-x", dst.ProjectId)
	assert.Equal(t, "my-net", dst.Name)
	assert.Equal(t, "desc", dst.Description)
	assert.Equal(t, "prod", dst.Labels["env"])
	assert.Equal(t, "enpsg", dst.DefaultSecurityGroupId)
	require.NotNil(t, dst.CreatedAt)
	// truncate до секунд.
	assert.Equal(t, at.Truncate(time.Second), dst.CreatedAt.AsTime())
}
