package toproto

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

// Network — legacy direct converter `domain.NetworkRecord → *vpcv1.Network`,
// перенесённый сюда из удалённого пакета `internal/protoconv` (Wave 5 / KAC-94,
// skill evgeniy §3 C.6). Используется handler_test (`TestNetworkToProto_Fields`).
// Новый production-call идёт через DTO-реестр (`dto.Transfer(dto.FromTo(...))`)
// в use-case-слое; этот helper существует только ради unit-теста и будет удалён,
// когда handler_test переедет на dto.Transfer.
//
// Контракт идентичен (network{}).toPb: CreatedAt truncate до секунд
// (verbatim YC — `YC-DIFF-TIMESTAMP-PRECISION`). Ошибки от time-трансфера
// игнорируются (timeObj{}.toPb на типичной time.Time не ошибается).
func Network(rec *domain.NetworkRecord) *vpcv1.Network {
	if rec == nil {
		return nil
	}
	p, _ := (network{}).toPb(*rec)
	return p
}
