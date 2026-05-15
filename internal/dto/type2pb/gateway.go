package type2pb

import (
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
)

// gateway — receiver-объект под трансфер domain.GatewayRecord → *vpcv1.Gateway.
// Wave 2 batch B (KAC-94), parity с network.go (pilot KAC-99).
type gateway struct{}

// toPb формирует *vpcv1.Gateway из repo-entity. CreatedAt — truncate до секунд
// через inline вызов time-трансфера. В YC verbatim Gateway имеет oneof spec
// (shared_egress_gateway) — выставляем всегда (единственный поддерживаемый тип).
func (gateway) toPb(rec domain.GatewayRecord) (*vpcv1.Gateway, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &vpcv1.Gateway{
		Id:          rec.ID,
		FolderId:    rec.FolderID,
		CreatedAt:   ts,
		Name:        string(rec.Name),
		Description: string(rec.Description),
		Labels:      domain.LabelsToMap(rec.Labels),
		// shared_egress — единственный поддерживаемый тип в YC sub-phase.
		Gateway: &vpcv1.Gateway_SharedEgressGateway{SharedEgressGateway: &vpcv1.SharedEgressGateway{}},
	}, nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(gateway{}.toPb))
}
