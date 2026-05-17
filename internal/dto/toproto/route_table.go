package toproto

import (
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// routeTable — receiver-объект под трансфер kacho.RouteTableRecord →
// *vpcv1.RouteTable. Wave 5 replicate (KAC-94): запись переехала из
// `domain.RouteTableRecord` в repo-leaf `kacho.RouteTableRecord` — §4 D.1.
type routeTable struct{}

// toPb формирует *vpcv1.RouteTable из repo-entity. CreatedAt — truncate до
// секунд через time-трансфер.
func (routeTable) toPb(rec kachorepo.RouteTableRecord) (*vpcv1.RouteTable, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	p := &vpcv1.RouteTable{
		Id:          rec.ID,
		ProjectId:    rec.ProjectID,
		CreatedAt:   ts,
		Name:        string(rec.Name),
		Description: string(rec.Description),
		Labels:      domain.LabelsToMap(rec.Labels),
		NetworkId:   rec.NetworkID,
	}
	for _, sr := range rec.StaticRoutes {
		psr := &vpcv1.StaticRoute{Labels: sr.Labels}
		if sr.DestinationPrefix != "" {
			psr.Destination = &vpcv1.StaticRoute_DestinationPrefix{DestinationPrefix: sr.DestinationPrefix}
		}
		if sr.NextHopAddress != "" {
			psr.NextHop = &vpcv1.StaticRoute_NextHopAddress{NextHopAddress: sr.NextHopAddress}
		}
		p.StaticRoutes = append(p.StaticRoutes, psr)
	}
	return p, nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(routeTable{}.toPb))
}
