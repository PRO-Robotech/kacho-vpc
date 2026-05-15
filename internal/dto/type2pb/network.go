package type2pb

import (
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
)

// network — receiver-объект под трансфер domain.NetworkRecord → *vpcv1.Network.
// Принимает repo-entity (NetworkRecord), потому что в pb-выходе требуется
// CreatedAt — он живёт в repo-проекции, не в domain.Network (skill evgeniy
// §3 C.3 / §4 D.1 / §7 H.1).
type network struct{}

// toPb формирует *vpcv1.Network из repo-entity. CreatedAt трансформируется
// через зарегистрированный time-трансфер (truncate до секунд).
func (network) toPb(rec domain.NetworkRecord) (*vpcv1.Network, error) {
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
		FolderId:               rec.FolderID,
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
