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

// Subnet конвертирует domain.Subnet → vpcv1.Subnet.
func Subnet(s *domain.Subnet) *vpcv1.Subnet {
	p := &vpcv1.Subnet{
		Id:           s.ID,
		FolderId:     s.FolderID,
		CreatedAt:    ts(s.CreatedAt),
		Name:         s.Name,
		Description:  s.Description,
		Labels:       s.Labels,
		NetworkId:    s.NetworkID,
		ZoneId:       s.ZoneID,
		V4CidrBlocks: s.V4CidrBlocks,
		V6CidrBlocks: s.V6CidrBlocks,
		RouteTableId: s.RouteTableID,
	}
	if s.DhcpOptions != nil {
		p.DhcpOptions = &vpcv1.DhcpOptions{
			DomainNameServers: s.DhcpOptions.DomainNameServers,
			DomainName:        s.DhcpOptions.DomainName,
			NtpServers:        s.DhcpOptions.NtpServers,
		}
	}
	return p
}

// Address конвертирует domain.Address → vpcv1.Address.
func Address(a *domain.Address) *vpcv1.Address {
	p := &vpcv1.Address{
		Id:                 a.ID,
		FolderId:           a.FolderID,
		CreatedAt:          ts(a.CreatedAt),
		Name:               a.Name,
		Description:        a.Description,
		Labels:             a.Labels,
		Reserved:           a.Reserved,
		Used:               a.Used,
		Type:               vpcv1.Address_Type(a.Type),
		IpVersion:          vpcv1.Address_IpVersion(a.IpVersion),
		DeletionProtection: a.DeletionProtection,
	}
	switch {
	case a.ExternalIpv4 != nil:
		ext := &vpcv1.ExternalIpv4Address{
			Address: a.ExternalIpv4.Address,
			ZoneId:  a.ExternalIpv4.ZoneID,
		}
		if a.ExternalIpv4.Requirements != nil {
			ext.Requirements = &vpcv1.AddressRequirements{
				DdosProtectionProvider: a.ExternalIpv4.Requirements.DdosProtectionProvider,
				OutgoingSmtpCapability: a.ExternalIpv4.Requirements.OutgoingSmtpCapability,
			}
		}
		p.Address = &vpcv1.Address_ExternalIpv4Address{ExternalIpv4Address: ext}
	case a.ExternalIpv6 != nil:
		// KAC-58: external IPv6.
		ext6 := &vpcv1.ExternalIpv6Address{
			Address: a.ExternalIpv6.Address,
			ZoneId:  a.ExternalIpv6.ZoneID,
		}
		if a.ExternalIpv6.Requirements != nil {
			ext6.Requirements = &vpcv1.AddressRequirements{
				DdosProtectionProvider: a.ExternalIpv6.Requirements.DdosProtectionProvider,
				OutgoingSmtpCapability: a.ExternalIpv6.Requirements.OutgoingSmtpCapability,
			}
		}
		p.Address = &vpcv1.Address_ExternalIpv6Address{ExternalIpv6Address: ext6}
	case a.InternalIpv6 != nil:
		p.Address = &vpcv1.Address_InternalIpv6Address{
			InternalIpv6Address: &vpcv1.InternalIpv6Address{
				Address: a.InternalIpv6.Address,
				Scope:   &vpcv1.InternalIpv6Address_SubnetId{SubnetId: a.InternalIpv6.SubnetID},
			},
		}
	case a.InternalIpv4 != nil:
		p.Address = &vpcv1.Address_InternalIpv4Address{
			InternalIpv4Address: &vpcv1.InternalIpv4Address{
				Address: a.InternalIpv4.Address,
				Scope:   &vpcv1.InternalIpv4Address_SubnetId{SubnetId: a.InternalIpv4.SubnetID},
			},
		}
	}
	// used_by (kacho extension, output-only) — кто использует адрес.
	// Shape совпадает с SubnetService UsedAddress.references[]: один Reference
	// с referrer{type,id} и type=USED_BY.
	for _, ref := range a.UsedBy {
		if ref == nil {
			continue
		}
		p.UsedBy = append(p.UsedBy, &reference.Reference{
			Referrer: &reference.Referrer{Type: ref.ReferrerType, Id: ref.ReferrerID},
			Type:     reference.Reference_USED_BY,
		})
	}
	return p
}

// RouteTable конвертирует domain.RouteTable → vpcv1.RouteTable.
func RouteTable(rt *domain.RouteTable) *vpcv1.RouteTable {
	p := &vpcv1.RouteTable{
		Id:          rt.ID,
		FolderId:    rt.FolderID,
		CreatedAt:   ts(rt.CreatedAt),
		Name:        rt.Name,
		Description: rt.Description,
		Labels:      rt.Labels,
		NetworkId:   rt.NetworkID,
	}
	for _, sr := range rt.StaticRoutes {
		psr := &vpcv1.StaticRoute{Labels: sr.Labels}
		if sr.DestinationPrefix != "" {
			psr.Destination = &vpcv1.StaticRoute_DestinationPrefix{DestinationPrefix: sr.DestinationPrefix}
		}
		if sr.NextHopAddress != "" {
			psr.NextHop = &vpcv1.StaticRoute_NextHopAddress{NextHopAddress: sr.NextHopAddress}
		}
		p.StaticRoutes = append(p.StaticRoutes, psr)
	}
	return p
}

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
