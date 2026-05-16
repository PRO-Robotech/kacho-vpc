package toproto

import (
	reference "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/reference"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
)

// networkInterface — receiver-объект под трансфер domain.NetworkInterfaceRecord →
// *vpcv1.NetworkInterface. Wave 2 batch C (KAC-94), parity с network.go (pilot KAC-99) /
// security_group.go (batch B). Принимает repo-entity (NetworkInterfaceRecord),
// потому что в pb-выходе требуется CreatedAt — он живёт в repo-проекции, не в
// domain.NetworkInterface (skill evgeniy §4 D.1 / §7 H.1).
type networkInterface struct{}

// toPb формирует *vpcv1.NetworkInterface из repo-entity. CreatedAt — truncate
// до секунд через inline вызов time-трансфера (verbatim YC `YC-DIFF-TIMESTAMP-PRECISION`).
//
// Публичная проекция — без data-plane-полей; раньше data-plane была в отдельной
// InternalNetworkInterface message, удалена в KAC-79/KAC-36 (post-kube-ovn).
func (networkInterface) toPb(rec domain.NetworkInterfaceRecord) (*vpcv1.NetworkInterface, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	p := &vpcv1.NetworkInterface{
		Id:               rec.ID,
		FolderId:         rec.FolderID,
		CreatedAt:        ts,
		Name:             string(rec.Name),
		Description:      string(rec.Description),
		Labels:           domain.LabelsToMap(rec.Labels),
		SubnetId:         rec.SubnetID,
		V4AddressIds:     rec.V4AddressIDs,
		V6AddressIds:     rec.V6AddressIDs,
		SecurityGroupIds: rec.SecurityGroupIDs,
		MacAddress:       rec.MAC,
		Status:           niStatusToPb(rec.Status),
	}
	// used_by (kacho extension, output-only) — кто приаттачил этот NIC.
	// Shape — как у Address.used_by: Reference{referrer{type,id}, type=USED_BY}.
	if rec.UsedByID != "" {
		p.UsedBy = &reference.Reference{
			Referrer: &reference.Referrer{Type: rec.UsedByType, Id: rec.UsedByID},
			Type:     reference.Reference_USED_BY,
		}
	}
	return p, nil
}

func niStatusToPb(s domain.NetworkInterfaceStatus) vpcv1.NetworkInterface_Status {
	switch s {
	case domain.NIStatusProvisioning:
		return vpcv1.NetworkInterface_PROVISIONING
	case domain.NIStatusActive:
		return vpcv1.NetworkInterface_ACTIVE
	case domain.NIStatusAvailable:
		return vpcv1.NetworkInterface_AVAILABLE
	case domain.NIStatusFailed:
		return vpcv1.NetworkInterface_FAILED
	case domain.NIStatusDeleting:
		return vpcv1.NetworkInterface_DELETING
	}
	return vpcv1.NetworkInterface_STATUS_UNSPECIFIED
}

func init() {
	dto.RegTransfer(dto.Fn2Face(networkInterface{}.toPb))
}
