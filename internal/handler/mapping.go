package handler

import (
	"strconv"

	commonv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/common/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// ---------- Network ----------

func protoNetworkToDomain(in *pb.Network) *domain.Network {
	net := &domain.Network{}
	if in == nil {
		return net
	}
	if m := in.GetMetadata(); m != nil {
		net.Name = m.GetName()
		net.FolderID = m.GetFolderId()
		net.CloudID = m.GetCloudId()
		net.OrganizationID = m.GetOrganizationId()
		net.Labels = m.GetLabels()
		net.Annotations = m.GetAnnotations()
	}
	if s := in.GetSpec(); s != nil {
		net.DisplayName = s.GetDisplayName()
		net.Description = s.GetDescription()
	}
	return net
}

func domainNetworkToProto(n *domain.Network) *pb.Network {
	meta := &commonv1.ResourceMeta{
		Uid:             n.UID,
		Name:            n.Name,
		FolderId:        n.FolderID,
		CloudId:         n.CloudID,
		OrganizationId:  n.OrganizationID,
		Labels:          n.Labels,
		Annotations:     n.Annotations,
		ResourceVersion: strconv.FormatInt(n.ResourceVersion, 10),
		Generation:      n.Generation,
		Finalizers:      n.Finalizers,
	}
	if !n.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(n.CreationTimestamp)
	}
	if n.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*n.DeletionTimestamp)
	}
	return &pb.Network{
		Metadata: meta,
		Spec:     &pb.NetworkSpec{DisplayName: n.DisplayName, Description: n.Description},
		Status:   &pb.NetworkStatus{State: n.State},
	}
}

// ---------- Subnet ----------

func protoSubnetToDomain(in *pb.Subnet) *domain.Subnet {
	subnet := &domain.Subnet{}
	if in == nil {
		return subnet
	}
	if m := in.GetMetadata(); m != nil {
		subnet.Name = m.GetName()
		subnet.FolderID = m.GetFolderId()
		subnet.Labels = m.GetLabels()
		subnet.Annotations = m.GetAnnotations()
	}
	if s := in.GetSpec(); s != nil {
		subnet.NetworkID = s.GetNetworkId()
		subnet.CIDRBlock = s.GetCidrBlock()
		subnet.ZoneID = s.GetZoneId()
		subnet.DisplayName = s.GetDisplayName()
		subnet.Description = s.GetDescription()
	}
	return subnet
}

func domainSubnetToProto(s *domain.Subnet) *pb.Subnet {
	meta := &commonv1.ResourceMeta{
		Uid:             s.UID,
		Name:            s.Name,
		FolderId:        s.FolderID,
		CloudId:         s.CloudID,
		OrganizationId:  s.OrganizationID,
		Labels:          s.Labels,
		Annotations:     s.Annotations,
		ResourceVersion: strconv.FormatInt(s.ResourceVersion, 10),
		Generation:      s.Generation,
		Finalizers:      s.Finalizers,
	}
	if !s.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(s.CreationTimestamp)
	}
	if s.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*s.DeletionTimestamp)
	}
	return &pb.Subnet{
		Metadata: meta,
		Spec: &pb.SubnetSpec{
			NetworkId:   s.NetworkID,
			CidrBlock:   s.CIDRBlock,
			ZoneId:      s.ZoneID,
			DisplayName: s.DisplayName,
			Description: s.Description,
		},
		Status: &pb.SubnetStatus{State: s.State},
	}
}

// ---------- SecurityGroup ----------

func protoSGToDomain(in *pb.SecurityGroup) *domain.SecurityGroup {
	sg := &domain.SecurityGroup{}
	if in == nil {
		return sg
	}
	if m := in.GetMetadata(); m != nil {
		sg.Name = m.GetName()
		sg.FolderID = m.GetFolderId()
		sg.Labels = m.GetLabels()
		sg.Annotations = m.GetAnnotations()
	}
	if s := in.GetSpec(); s != nil {
		sg.NetworkID = s.GetNetworkId()
		sg.DisplayName = s.GetDisplayName()
		sg.Description = s.GetDescription()
		for _, r := range s.GetRules() {
			sg.Rules = append(sg.Rules, domain.SecurityGroupRule{
				Direction:    r.GetDirection(),
				Protocol:     r.GetProtocol(),
				PortRangeMin: r.GetPortRangeMin(),
				PortRangeMax: r.GetPortRangeMax(),
				CIDRBlocks:   r.GetCidrBlocks(),
				Description:  r.GetDescription(),
			})
		}
	}
	return sg
}

func domainSGToProto(sg *domain.SecurityGroup) *pb.SecurityGroup {
	meta := &commonv1.ResourceMeta{
		Uid:             sg.UID,
		Name:            sg.Name,
		FolderId:        sg.FolderID,
		CloudId:         sg.CloudID,
		OrganizationId:  sg.OrganizationID,
		Labels:          sg.Labels,
		Annotations:     sg.Annotations,
		ResourceVersion: strconv.FormatInt(sg.ResourceVersion, 10),
		Generation:      sg.Generation,
		Finalizers:      sg.Finalizers,
	}
	if !sg.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(sg.CreationTimestamp)
	}
	if sg.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*sg.DeletionTimestamp)
	}

	rules := make([]*pb.SecurityGroupRule, len(sg.Rules))
	for i, r := range sg.Rules {
		rules[i] = &pb.SecurityGroupRule{
			Id:           r.ID,
			Direction:    r.Direction,
			Protocol:     r.Protocol,
			PortRangeMin: r.PortRangeMin,
			PortRangeMax: r.PortRangeMax,
			CidrBlocks:   r.CIDRBlocks,
			Description:  r.Description,
		}
	}
	return &pb.SecurityGroup{
		Metadata: meta,
		Spec: &pb.SecurityGroupSpec{
			NetworkId:   sg.NetworkID,
			DisplayName: sg.DisplayName,
			Description: sg.Description,
			Rules:       rules,
		},
		Status: &pb.SecurityGroupStatus{State: sg.State},
	}
}

// ---------- RouteTable ----------

func protoRTToDomain(in *pb.RouteTable) *domain.RouteTable {
	rt := &domain.RouteTable{}
	if in == nil {
		return rt
	}
	if m := in.GetMetadata(); m != nil {
		rt.Name = m.GetName()
		rt.FolderID = m.GetFolderId()
		rt.Labels = m.GetLabels()
		rt.Annotations = m.GetAnnotations()
	}
	if s := in.GetSpec(); s != nil {
		rt.NetworkID = s.GetNetworkId()
		rt.DisplayName = s.GetDisplayName()
		rt.Description = s.GetDescription()
		for _, r := range s.GetStaticRoutes() {
			rt.StaticRoutes = append(rt.StaticRoutes, domain.StaticRoute{
				DestinationPrefix: r.GetDestinationPrefix(),
				NextHopAddress:    r.GetNextHopAddress(),
				Description:       r.GetDescription(),
			})
		}
	}
	return rt
}

func domainRTToProto(rt *domain.RouteTable) *pb.RouteTable {
	meta := &commonv1.ResourceMeta{
		Uid:             rt.UID,
		Name:            rt.Name,
		FolderId:        rt.FolderID,
		CloudId:         rt.CloudID,
		OrganizationId:  rt.OrganizationID,
		Labels:          rt.Labels,
		Annotations:     rt.Annotations,
		ResourceVersion: strconv.FormatInt(rt.ResourceVersion, 10),
		Generation:      rt.Generation,
		Finalizers:      rt.Finalizers,
	}
	if !rt.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(rt.CreationTimestamp)
	}
	if rt.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*rt.DeletionTimestamp)
	}

	routes := make([]*pb.StaticRoute, len(rt.StaticRoutes))
	for i, r := range rt.StaticRoutes {
		routes[i] = &pb.StaticRoute{
			Id:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}
	return &pb.RouteTable{
		Metadata: meta,
		Spec: &pb.RouteTableSpec{
			NetworkId:    rt.NetworkID,
			DisplayName:  rt.DisplayName,
			Description:  rt.Description,
			StaticRoutes: routes,
		},
		Status: &pb.RouteTableStatus{State: rt.State},
	}
}

// ---------- Address ----------

func protoAddrToDomain(in *pb.Address) *domain.Address {
	addr := &domain.Address{}
	if in == nil {
		return addr
	}
	if m := in.GetMetadata(); m != nil {
		addr.Name = m.GetName()
		addr.FolderID = m.GetFolderId()
		addr.Labels = m.GetLabels()
		addr.Annotations = m.GetAnnotations()
	}
	if s := in.GetSpec(); s != nil {
		addr.AddressType = s.GetAddressType()
		addr.ZoneID = s.GetZoneId()
		addr.DisplayName = s.GetDisplayName()
		addr.Description = s.GetDescription()
	}
	return addr
}

func domainAddrToProto(a *domain.Address) *pb.Address {
	meta := &commonv1.ResourceMeta{
		Uid:             a.UID,
		Name:            a.Name,
		FolderId:        a.FolderID,
		CloudId:         a.CloudID,
		OrganizationId:  a.OrganizationID,
		Labels:          a.Labels,
		Annotations:     a.Annotations,
		ResourceVersion: strconv.FormatInt(a.ResourceVersion, 10),
		Generation:      a.Generation,
		Finalizers:      a.Finalizers,
	}
	if !a.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(a.CreationTimestamp)
	}
	if a.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*a.DeletionTimestamp)
	}
	return &pb.Address{
		Metadata: meta,
		Spec: &pb.AddressSpec{
			AddressType: a.AddressType,
			ZoneId:      a.ZoneID,
			DisplayName: a.DisplayName,
			Description: a.Description,
		},
		Status: &pb.AddressStatus{
			State:         a.State,
			AllocatedIpv4: a.AllocatedIPv4,
		},
	}
}

// ---------- Selectors ----------

func protoSelectorsToService(pbSelectors []*commonv1.Selector) []service.Selector {
	var result []service.Selector
	for _, s := range pbSelectors {
		sel := service.Selector{Labels: s.GetLabelSelector()}
		if f := s.GetFieldSelector(); f != nil {
			sel.Name = f.GetName()
			sel.FolderID = f.GetFolderId()
			sel.CloudID = f.GetCloudId()
			sel.OrganizationID = f.GetOrganizationId()
		}
		result = append(result, sel)
	}
	return result
}

func protoSelectorsToServiceWithNetwork(pbSelectors []*commonv1.Selector) []service.Selector {
	var result []service.Selector
	for _, s := range pbSelectors {
		sel := service.Selector{Labels: s.GetLabelSelector()}
		if f := s.GetFieldSelector(); f != nil {
			sel.Name = f.GetName()
			sel.FolderID = f.GetFolderId()
			sel.CloudID = f.GetCloudId()
			sel.OrganizationID = f.GetOrganizationId()
		}
		// C6: поддержка refs[kind=Network, uid=X] через field_selector.refs
		if f := s.GetFieldSelector(); f != nil {
			for _, ref := range f.GetRefs() {
				if ref.GetKind() == "Network" {
					sel.NetworkID = ref.GetUid()
				}
			}
		}
		result = append(result, sel)
	}
	return result
}
