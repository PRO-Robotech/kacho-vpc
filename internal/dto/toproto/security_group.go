package toproto

import (
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// securityGroup — receiver-объект под трансфер kachorepo.SecurityGroupRecord →
// *vpcv1.SecurityGroup. Wave 5 D.1 (KAC-94): запись переехала в repo-leaf
// `internal/repo/kacho/entity_security_group.go`.
type securityGroup struct{}

// toPb формирует *vpcv1.SecurityGroup из repo-entity. CreatedAt — truncate до
// секунд через inline вызов time-трансфера (verbatim YC `YC-DIFF-TIMESTAMP-PRECISION`).
func (securityGroup) toPb(rec kachorepo.SecurityGroupRecord) (*vpcv1.SecurityGroup, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	p := &vpcv1.SecurityGroup{
		Id:                rec.ID,
		ProjectId:         rec.ProjectID,
		NetworkId:         rec.NetworkID,
		CreatedAt:         ts,
		Name:              string(rec.Name),
		Description:       string(rec.Description),
		Labels:            domain.LabelsToMap(rec.Labels),
		Status:            sgStatusToPb(rec.Status),
		DefaultForNetwork: rec.DefaultForNetwork,
	}
	for _, r := range rec.Rules {
		pr := &vpcv1.SecurityGroupRule{
			Id:             r.ID,
			Description:    string(r.Description),
			Labels:         r.Labels,
			Direction:      sgDirectionToPb(r.Direction),
			ProtocolName:   r.ProtocolName,
			ProtocolNumber: r.ProtocolNumber,
		}
		if r.FromPort != 0 || r.ToPort != 0 {
			pr.Ports = &vpcv1.PortRange{FromPort: r.FromPort, ToPort: r.ToPort}
		}
		if len(r.V4CidrBlocks) > 0 || len(r.V6CidrBlocks) > 0 {
			pr.Target = &vpcv1.SecurityGroupRule_CidrBlocks{
				CidrBlocks: &vpcv1.CidrBlocks{
					V4CidrBlocks: r.V4CidrBlocks,
					V6CidrBlocks: r.V6CidrBlocks,
				},
			}
		}
		p.Rules = append(p.Rules, pr)
	}
	return p, nil
}

// sgStatusToPb маппит domain-статус SecurityGroup в proto-enum SecurityGroup_Status.
func sgStatusToPb(s domain.SecurityGroupStatus) vpcv1.SecurityGroup_Status {
	switch s {
	case domain.SecurityGroupStatusCreating:
		return vpcv1.SecurityGroup_CREATING
	case domain.SecurityGroupStatusActive:
		return vpcv1.SecurityGroup_ACTIVE
	case domain.SecurityGroupStatusUpdating:
		return vpcv1.SecurityGroup_UPDATING
	case domain.SecurityGroupStatusDeleting:
		return vpcv1.SecurityGroup_DELETING
	}
	return vpcv1.SecurityGroup_STATUS_UNSPECIFIED
}

// sgDirectionToPb маппит domain-направление SG-правила в proto-enum SecurityGroupRule_Direction.
func sgDirectionToPb(d domain.SecurityGroupRuleDirection) vpcv1.SecurityGroupRule_Direction {
	switch d {
	case domain.SecurityGroupRuleDirectionIngress:
		return vpcv1.SecurityGroupRule_INGRESS
	case domain.SecurityGroupRuleDirectionEgress:
		return vpcv1.SecurityGroupRule_EGRESS
	}
	return vpcv1.SecurityGroupRule_DIRECTION_UNSPECIFIED
}

func init() {
	dto.RegTransfer(dto.Fn2Face(securityGroup{}.toPb))
}
