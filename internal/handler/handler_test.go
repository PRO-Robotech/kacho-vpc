// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// blank-import регистрирует в DTO-реестре трансфер
	// kachorepo.NetworkRecord → *vpcv1.Network.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Fake-реализации port-ов и await-helper'ы — в `internal/repo/repomock`
// (shim с прежними именами — в mock_test.go). Network-handler-тесты живут в
// use-case-пакете `internal/apps/kacho/api/network/usecase_test.go`.

// ---- tests ----

func TestNetworkToProto_Fields(t *testing.T) {
	// Production-маппинг Network идет через DTO-реестр; тест валидирует тот же
	// контракт через `dto.Transfer(dto.FromTo(rec, &dst))`. NetworkRecord живет
	// в `internal/repo/kacho/`.
	rec := kachorepo.NetworkRecord{
		Network: domain.Network{
			ID:          "net-123",
			ProjectID:   "project-1",
			Name:        domain.RcNameVPC("my-net"),
			Description: domain.RcDescription("desc"),
			Labels:      domain.LabelsFromMap(map[string]string{"env": "test"}),
		},
	}
	var p *vpcv1.Network
	require.NoError(t, dto.Transfer(dto.FromTo(rec, &p)))
	require.NotNil(t, p)
	assert.Equal(t, "net-123", p.Id)
	assert.Equal(t, "project-1", p.ProjectId)
	assert.Equal(t, "my-net", p.Name)
	assert.Equal(t, "desc", p.Description)
	assert.Equal(t, "test", p.Labels["env"])
}

// Тесты addressToPb (External/Internal) живут в use-case-пакете
// `internal/apps/kacho/api/address/usecase_test.go`.

// Тест RouteTable static-routes живет в use-case-пакете
// `internal/apps/kacho/api/routetable/usecase_test.go`.

// Тест subnetToPb (cidr-blocks) живет в use-case-пакете
// `internal/apps/kacho/api/subnet/usecase_test.go`.
