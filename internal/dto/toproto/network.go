// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package toproto

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// network — receiver-объект под трансфер kachorepo.NetworkRecord → *vpcv1.Network.
// Принимает repo-entity (NetworkRecord), потому что в pb-выходе требуется
// CreatedAt — он живет в repo-проекции, не в domain.Network. DTO — мост между
// repo-entity и proto, поэтому импорт kachorepo здесь уместен.
type network struct{}

// toPb формирует *vpcv1.Network из repo-entity. CreatedAt трансформируется
// через зарегистрированный time-трансфер (truncate до секунд).
func (network) toPb(rec kachorepo.NetworkRecord) (*vpcv1.Network, error) {
	var createdAt = rec.CreatedAt
	// Inline-вызов time-трансфера; нет смысла в Transfer(...) для одного
	// timestamp-поля внутри одного маппинга — это создало бы лишнюю
	// аллокацию на каждый Network.toPb.
	ts, err := (timeObj{}).toPb(createdAt)
	if err != nil {
		return nil, err
	}
	return &vpcv1.Network{
		Id:                     rec.ID,
		ProjectId:              rec.ProjectID,
		CreatedAt:              ts,
		Name:                   string(rec.Name),
		Description:            string(rec.Description),
		Labels:                 domain.LabelsToMap(rec.Labels),
		DefaultSecurityGroupId: rec.DefaultSecurityGroupID,
	}, nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(network{}.toPb))
}
