package toproto

import (
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// subnet — receiver-объект под трансфер kachorepo.SubnetRecord → *vpcv1.Subnet.
// Wave 2 batch A (KAC-94), parity с network.go (pilot KAC-99).
type subnet struct{}

// toPb формирует *vpcv1.Subnet из repo-entity. CreatedAt — truncate до секунд
// через inline вызов time-трансфера (verbatim YC `YC-DIFF-TIMESTAMP-PRECISION`).
func (subnet) toPb(rec kachorepo.SubnetRecord) (*vpcv1.Subnet, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	p := &vpcv1.Subnet{
		Id:           rec.ID,
		ProjectId:     rec.ProjectID,
		CreatedAt:    ts,
		Name:         string(rec.Name),
		Description:  string(rec.Description),
		Labels:       domain.LabelsToMap(rec.Labels),
		NetworkId:    rec.NetworkID,
		ZoneId:       rec.ZoneID,
		V4CidrBlocks: rec.V4CidrBlocks,
		V6CidrBlocks: rec.V6CidrBlocks,
		RouteTableId: rec.RouteTableID,
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

func init() {
	dto.RegTransfer(dto.Fn2Face(subnet{}.toPb))
}
