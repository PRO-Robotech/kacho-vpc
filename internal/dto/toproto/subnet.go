// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package toproto

import (
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// subnet — receiver-объект под трансфер kachorepo.SubnetRecord → *vpcv1.Subnet.
type subnet struct{}

// toPb формирует *vpcv1.Subnet из repo-entity. CreatedAt — truncate до секунд
// через inline вызов time-трансфера.
func (subnet) toPb(rec kachorepo.SubnetRecord) (*vpcv1.Subnet, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	p := &vpcv1.Subnet{
		Id:            rec.ID,
		ProjectId:     rec.ProjectID,
		CreatedAt:     ts,
		Name:          string(rec.Name),
		Description:   string(rec.Description),
		Labels:        domain.LabelsToMap(rec.Labels),
		NetworkId:     rec.NetworkID,
		PlacementType: placementToPb(rec.PlacementType),
		ZoneId:        rec.ZoneID,
		RegionId:      rec.RegionID,
		V4CidrBlocks:  rec.V4CidrBlocks,
		V6CidrBlocks:  rec.V6CidrBlocks,
		RouteTableId:  rec.RouteTableID,
	}
	if rec.DhcpOptions != nil {
		p.DhcpOptions = &vpcv1.DhcpOptions{
			DomainNameServers: rec.DhcpOptions.DomainNameServers,
			DomainName:        rec.DhcpOptions.DomainName,
			NtpServers:        rec.DhcpOptions.NtpServers,
		}
	}
	return p, nil
}

// placementToPb — domain-дискриминатор → proto-enum. Пустое (UNSPECIFIED)
// доменное значение в выдаче не появляется (CHECK гарантирует ZONAL/REGIONAL),
// но маппится в UNSPECIFIED defensively.
func placementToPb(p domain.SubnetPlacementType) vpcv1.SubnetPlacementType {
	switch p {
	case domain.PlacementZonal:
		return vpcv1.SubnetPlacementType_ZONAL
	case domain.PlacementRegional:
		return vpcv1.SubnetPlacementType_REGIONAL
	default:
		return vpcv1.SubnetPlacementType_SUBNET_PLACEMENT_TYPE_UNSPECIFIED
	}
}

func init() {
	dto.RegTransfer(dto.Fn2Face(subnet{}.toPb))
}
