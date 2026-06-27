// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package toproto

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// gateway — receiver-объект под трансфер kacho.GatewayRecord → *vpcv1.Gateway.
// Parity с network.go.
type gateway struct{}

// toPb формирует *vpcv1.Gateway из repo-entity. CreatedAt — truncate до секунд
// через inline вызов time-трансфера. У Gateway oneof spec (shared_egress_gateway)
// выставляем всегда — это единственный поддерживаемый тип.
func (gateway) toPb(rec kacho.GatewayRecord) (*vpcv1.Gateway, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &vpcv1.Gateway{
		Id:          rec.ID,
		ProjectId:   rec.ProjectID,
		CreatedAt:   ts,
		Name:        string(rec.Name),
		Description: string(rec.Description),
		Labels:      domain.LabelsToMap(rec.Labels),
		// shared_egress — единственный поддерживаемый тип.
		Gateway: &vpcv1.Gateway_SharedEgressGateway{SharedEgressGateway: &vpcv1.SharedEgressGateway{}},
	}, nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(gateway{}.toPb))
}
