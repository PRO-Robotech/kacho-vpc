package toproto

import (
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
)

// privateEndpoint — receiver-объект под трансфер domain.PrivateEndpointRecord →
// *pepb.PrivateEndpoint. Wave 2 batch B (KAC-94).
type privateEndpoint struct{}

// toPb формирует *pepb.PrivateEndpoint из repo-entity. CreatedAt — truncate до
// секунд через time-трансфер.
func (privateEndpoint) toPb(rec domain.PrivateEndpointRecord) (*pepb.PrivateEndpoint, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	out := &pepb.PrivateEndpoint{
		Id:          rec.ID,
		FolderId:    rec.FolderID,
		CreatedAt:   ts,
		Name:        string(rec.Name),
		Description: string(rec.Description),
		Labels:      domain.LabelsToMap(rec.Labels),
		NetworkId:   rec.NetworkID,
		Status:      peStatusToPb(rec.Status),
	}
	if rec.SubnetID != "" || rec.IPAddress != "" || rec.AddressID != "" {
		out.Address = &pepb.PrivateEndpoint_EndpointAddress{
			SubnetId:  rec.SubnetID,
			Address:   rec.IPAddress,
			AddressId: rec.AddressID,
		}
	}
	if v, ok := rec.DnsOptions["private_dns_records_enabled"]; ok {
		if b, ok := v.(bool); ok {
			out.DnsOptions = &pepb.PrivateEndpoint_DnsOptions{PrivateDnsRecordsEnabled: b}
		}
	}
	// Service oneof — только object_storage в текущей фазе.
	if rec.ServiceType == domain.PrivateEndpointServiceTypeObjectStorage || rec.ServiceType == "" {
		out.Service = &pepb.PrivateEndpoint_ObjectStorage_{ObjectStorage: &pepb.PrivateEndpoint_ObjectStorage{}}
	}
	return out, nil
}

func peStatusToPb(s domain.PrivateEndpointStatus) pepb.PrivateEndpoint_Status {
	switch s {
	case domain.PrivateEndpointStatusPending:
		return pepb.PrivateEndpoint_PENDING
	case domain.PrivateEndpointStatusAvailable:
		return pepb.PrivateEndpoint_AVAILABLE
	case domain.PrivateEndpointStatusDeleting:
		return pepb.PrivateEndpoint_DELETING
	}
	return pepb.PrivateEndpoint_STATUS_UNSPECIFIED
}

func init() {
	dto.RegTransfer(dto.Fn2Face(privateEndpoint{}.toPb))
}
