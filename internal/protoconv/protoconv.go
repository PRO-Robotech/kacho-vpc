// Package protoconv — единственное место конверсии domain-сущностей VPC в proto-сообщения.
//
// Зачем: раньше каждый ресурс имел ДВЕ копии конвертера — `domainXToProto` в
// service-слое (для `Operation.response`) и `xToProto` в handler-слое (для Get/List).
// Копии разъехались: handler-версии ставили `created_at` (truncate до секунд),
// service-версии — нет → клиент, читающий `Operation.response`, получал `created_at == null`,
// а тот же ресурс через Get — с заполненным `created_at` (расхождение с verbatim YC).
// Теперь конвертер один; и service, и handler зовут `protoconv.X(...)`.
//
// Контракт: `created_at` всегда truncate до секунд (verbatim YC — `YC-DIFF-TIMESTAMP-PRECISION`).
package protoconv

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	reference "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/reference"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t.Truncate(time.Second)) }

// Network конвертирует repo-entity Network → vpcv1.Network.
//
// Wave 2 pilot (KAC-99/KAC-94): принимает *domain.NetworkRecord (repo-entity
// с CreatedAt), а не *domain.Network — CreatedAt уехал в repo-projection
// (skill evgeniy §4 D.1 / §7 H.1). Это legacy-функция, оставлена ради тестов
// (handler_test и т.п.). Новый production-call идёт через DTO-реестр
// (`dto.Transfer(dto.FromTo(...))`) в `service/network.go::marshalNetworkRecord`
// и `handler/network_handler.go::networkToPb`. См. skill §3 C.3 / AP-11.
func Network(rec *domain.NetworkRecord) *vpcv1.Network {
	return &vpcv1.Network{
		Id:                     rec.ID,
		FolderId:               rec.FolderID,
		CreatedAt:              ts(rec.CreatedAt),
		Name:                   string(rec.Name),
		Description:            string(rec.Description),
		Labels:                 domain.LabelsToMap(rec.Labels),
		DefaultSecurityGroupId: rec.DefaultSecurityGroupID,
	}
}

// NetworkInterface конвертирует domain.NetworkInterface → vpcv1.NetworkInterface.
// Публичная проекция — без data-plane-полей; раньше data-plane была в отдельной
// InternalNetworkInterface message, удалена в KAC-79/KAC-36 (post-kube-ovn).
func NetworkInterface(n *domain.NetworkInterface) *vpcv1.NetworkInterface {
	p := &vpcv1.NetworkInterface{
		Id:               n.ID,
		FolderId:         n.FolderID,
		CreatedAt:        ts(n.CreatedAt),
		Name:             n.Name,
		Description:      n.Description,
		Labels:           n.Labels,
		SubnetId:         n.SubnetID,
		V4AddressIds:     n.V4AddressIDs,
		V6AddressIds:     n.V6AddressIDs,
		SecurityGroupIds: n.SecurityGroupIDs,
		MacAddress:       n.MAC,
		Status:           vpcv1.NetworkInterface_Status(n.Status),
	}
	// used_by (kacho extension, output-only) — кто приаттачил этот NIC.
	// Shape — как у Address.used_by: Reference{referrer{type,id}, type=USED_BY}.
	if n.UsedByID != "" {
		p.UsedBy = &reference.Reference{
			Referrer: &reference.Referrer{Type: n.UsedByType, Id: n.UsedByID},
			Type:     reference.Reference_USED_BY,
		}
	}
	return p
}

// Subnet/Address/RouteTable конверторы перенесены в `internal/dto/type2pb/`
// (Wave 2 batch A, KAC-94, skill evgeniy §3 C.6 / AP-11). См. вызов через
// `dto.Transfer(dto.FromTo(...))` в service/handler.

// SecurityGroup конвертирует domain.SecurityGroup → vpcv1.SecurityGroup.
func SecurityGroup(sg *domain.SecurityGroup) *vpcv1.SecurityGroup {
	p := &vpcv1.SecurityGroup{
		Id:                sg.ID,
		FolderId:          sg.FolderID,
		NetworkId:         sg.NetworkID,
		CreatedAt:         ts(sg.CreatedAt),
		Name:              sg.Name,
		Description:       sg.Description,
		Labels:            sg.Labels,
		Status:            sgStatus(sg.Status),
		DefaultForNetwork: sg.DefaultForNetwork,
	}
	for _, r := range sg.Rules {
		pr := &vpcv1.SecurityGroupRule{
			Id:             r.ID,
			Description:    r.Description,
			Labels:         r.Labels,
			Direction:      sgDirection(r.Direction),
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
	return p
}

func sgStatus(s string) vpcv1.SecurityGroup_Status {
	switch s {
	case "CREATING":
		return vpcv1.SecurityGroup_CREATING
	case "ACTIVE":
		return vpcv1.SecurityGroup_ACTIVE
	case "UPDATING":
		return vpcv1.SecurityGroup_UPDATING
	case "DELETING":
		return vpcv1.SecurityGroup_DELETING
	}
	return vpcv1.SecurityGroup_STATUS_UNSPECIFIED
}

func sgDirection(d string) vpcv1.SecurityGroupRule_Direction {
	switch d {
	case "INGRESS":
		return vpcv1.SecurityGroupRule_INGRESS
	case "EGRESS":
		return vpcv1.SecurityGroupRule_EGRESS
	}
	return vpcv1.SecurityGroupRule_DIRECTION_UNSPECIFIED
}

// Gateway конвертирует domain.Gateway → vpcv1.Gateway.
func Gateway(g *domain.Gateway) *vpcv1.Gateway {
	return &vpcv1.Gateway{
		Id:          g.ID,
		FolderId:    g.FolderID,
		CreatedAt:   ts(g.CreatedAt),
		Name:        g.Name,
		Description: g.Description,
		Labels:      g.Labels,
		// shared_egress — единственный поддерживаемый тип в YC sub-phase.
		Gateway: &vpcv1.Gateway_SharedEgressGateway{SharedEgressGateway: &vpcv1.SharedEgressGateway{}},
	}
}

// PrivateEndpoint конвертирует domain.PrivateEndpoint → pepb.PrivateEndpoint.
func PrivateEndpoint(p *domain.PrivateEndpoint) *pepb.PrivateEndpoint {
	out := &pepb.PrivateEndpoint{
		Id:          p.ID,
		FolderId:    p.FolderID,
		CreatedAt:   ts(p.CreatedAt),
		Name:        p.Name,
		Description: p.Description,
		Labels:      p.Labels,
		NetworkId:   p.NetworkID,
	}
	switch p.Status {
	case "PENDING":
		out.Status = pepb.PrivateEndpoint_PENDING
	case "AVAILABLE":
		out.Status = pepb.PrivateEndpoint_AVAILABLE
	case "DELETING":
		out.Status = pepb.PrivateEndpoint_DELETING
	default:
		out.Status = pepb.PrivateEndpoint_STATUS_UNSPECIFIED
	}
	if p.SubnetID != "" || p.IPAddress != "" || p.AddressID != "" {
		out.Address = &pepb.PrivateEndpoint_EndpointAddress{
			SubnetId:  p.SubnetID,
			Address:   p.IPAddress,
			AddressId: p.AddressID,
		}
	}
	if v, ok := p.DnsOptions["private_dns_records_enabled"]; ok {
		if b, ok := v.(bool); ok {
			out.DnsOptions = &pepb.PrivateEndpoint_DnsOptions{PrivateDnsRecordsEnabled: b}
		}
	}
	// Service oneof — только object_storage в текущей фазе.
	if p.ServiceType == "object_storage" || p.ServiceType == "" {
		out.Service = &pepb.PrivateEndpoint_ObjectStorage_{ObjectStorage: &pepb.PrivateEndpoint_ObjectStorage{}}
	}
	return out
}
