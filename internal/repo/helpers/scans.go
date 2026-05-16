package helpers

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Scannable — минимальный pgx-row-интерфейс для scan-функций. Совместим
// с pgx.Row и `tx.QueryRow(...)`/`rows.Next()+rows.Scan(...)`.
type Scannable interface {
	Scan(dest ...any) error
}

// ---------- Column-list константы ----------

// NetworkCols — список колонок таблицы networks в порядке, ожидаемом ScanNetwork.
const NetworkCols = `id, folder_id, created_at, name, description, labels, default_security_group_id`

// SubnetCols — список колонок таблицы subnets в порядке, ожидаемом ScanSubnet.
const SubnetCols = `id, folder_id, created_at, name, description, labels, network_id, zone_id, v4_cidr_blocks, v6_cidr_blocks, route_table_id, dhcp_options`

// AddressCols — список колонок таблицы addresses в порядке, ожидаемом ScanAddress.
const AddressCols = `id, folder_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4, internal_ipv6, external_ipv6`

// RouteTableCols — список колонок таблицы route_tables в порядке, ожидаемом ScanRouteTable.
const RouteTableCols = `id, folder_id, created_at, name, description, labels, network_id, static_routes`

// SGCols — список колонок таблицы security_groups в порядке, ожидаемом ScanSG.
const SGCols = `id, folder_id, network_id, created_at, name, description, labels, status, default_for_network, rules`

// GatewayCols — список колонок таблицы gateways в порядке, ожидаемом ScanGateway.
const GatewayCols = `id, folder_id, created_at, name, description, labels, gateway_type`

// PECols — список колонок таблицы private_endpoints в порядке, ожидаемом ScanPrivateEndpoint.
const PECols = `id, folder_id, created_at, name, description, labels, network_id, subnet_id, address_id, ip_address, service_type, dns_options, status`

// NICCols — список колонок таблицы network_interfaces в порядке, ожидаемом ScanNI.
const NICCols = `id, folder_id, created_at, name, description, labels, subnet_id,
	v4_address_ids, v6_address_ids, security_group_ids, used_by_type, used_by_id, used_by_name, mac_address, status`

// AddressPoolCols — список колонок address_pools в порядке, ожидаемом ScanAddressPool.
const AddressPoolCols = `id, name, description, labels, v4_cidr_blocks, v6_cidr_blocks, kind, zone_id, is_default, selector_labels, selector_priority, created_at, modified_at`

// ---------- Scan-функции ----------

// ScanNetwork — row-scanner для NetworkRecord.
func ScanNetwork(row Scannable) (*kachorepo.NetworkRecord, error) {
	var n kachorepo.NetworkRecord
	var labelsJSON []byte
	var name string
	var description string

	err := row.Scan(
		&n.ID, &n.FolderID, &n.CreatedAt, &name, &description, &labelsJSON,
		&n.DefaultSecurityGroupID,
	)
	if err != nil {
		return nil, err
	}
	n.Name = domain.RcNameVPC(name)
	n.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "Network.labels"); err != nil {
		return nil, err
	}
	n.Labels = domain.LabelsFromMap(labels)
	return &n, nil
}

// ScanSubnet — row-scanner для SubnetRecord.
func ScanSubnet(row Scannable) (*kachorepo.SubnetRecord, error) {
	var s kachorepo.SubnetRecord
	var labelsJSON, dhcpJSON []byte
	var v4, v6 pgtype.Array[string]
	var routeTableID *string
	var name string
	var description string

	err := row.Scan(
		&s.ID, &s.FolderID, &s.CreatedAt, &name, &description, &labelsJSON,
		&s.NetworkID, &s.ZoneID, &v4, &v6, &routeTableID, &dhcpJSON,
	)
	if err != nil {
		return nil, err
	}
	s.Name = domain.RcNameVPC(name)
	s.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "Subnet.labels"); err != nil {
		return nil, err
	}
	s.Labels = domain.LabelsFromMap(labels)
	if v4.Valid {
		s.V4CidrBlocks = v4.Elements
	}
	if v6.Valid {
		s.V6CidrBlocks = v6.Elements
	}
	if routeTableID != nil {
		s.RouteTableID = *routeTableID
	}
	if dhcpJSON != nil {
		var dhcp domain.DhcpOptions
		if err := UnmarshalJSONB(dhcpJSON, &dhcp, "Subnet.dhcp_options"); err != nil {
			return nil, err
		}
		s.DhcpOptions = &dhcp
	}
	return &s, nil
}

// ScanAddress — row-scanner для AddressRecord.
func ScanAddress(row Scannable) (*kachorepo.AddressRecord, error) {
	var a kachorepo.AddressRecord
	var labelsJSON, extJSON, intJSON, int6JSON, ext6JSON []byte
	var addrType, ipVersion int32
	var name string
	var description string

	err := row.Scan(
		&a.ID, &a.FolderID, &a.CreatedAt, &name, &description, &labelsJSON,
		&addrType, &ipVersion, &a.Reserved, &a.Used, &a.DeletionProtection,
		&extJSON, &intJSON, &int6JSON, &ext6JSON,
	)
	if err != nil {
		return nil, err
	}
	a.Name = domain.RcNameVPC(name)
	a.Description = domain.RcDescription(description)
	a.Type = domain.AddressType(addrType)
	a.IpVersion = domain.IpVersion(ipVersion)

	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "Address.labels"); err != nil {
		return nil, err
	}
	a.Labels = domain.LabelsFromMap(labels)
	if extJSON != nil {
		var ext domain.ExternalIpv4Spec
		if err := UnmarshalJSONB(extJSON, &ext, "Address.external_ipv4"); err != nil {
			return nil, err
		}
		a.ExternalIpv4 = &ext
	}
	if intJSON != nil {
		var intSpec domain.InternalIpv4Spec
		if err := UnmarshalJSONB(intJSON, &intSpec, "Address.internal_ipv4"); err != nil {
			return nil, err
		}
		a.InternalIpv4 = &intSpec
	}
	if int6JSON != nil {
		var int6Spec domain.InternalIpv6Spec
		if err := UnmarshalJSONB(int6JSON, &int6Spec, "Address.internal_ipv6"); err != nil {
			return nil, err
		}
		a.InternalIpv6 = &int6Spec
	}
	if ext6JSON != nil {
		var ext6 domain.ExternalIpv6Spec
		if err := UnmarshalJSONB(ext6JSON, &ext6, "Address.external_ipv6"); err != nil {
			return nil, err
		}
		a.ExternalIpv6 = &ext6
	}
	return &a, nil
}

// ScanRouteTable — row-scanner для RouteTableRecord.
func ScanRouteTable(row Scannable) (*kachorepo.RouteTableRecord, error) {
	var rt kachorepo.RouteTableRecord
	var labelsJSON, routesJSON []byte
	var name string
	var description string

	err := row.Scan(
		&rt.ID, &rt.FolderID, &rt.CreatedAt, &name, &description, &labelsJSON,
		&rt.NetworkID, &routesJSON,
	)
	if err != nil {
		return nil, err
	}
	rt.Name = domain.RcNameVPC(name)
	rt.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "RouteTable.labels"); err != nil {
		return nil, err
	}
	rt.Labels = domain.LabelsFromMap(labels)
	if err := UnmarshalJSONB(routesJSON, &rt.StaticRoutes, "RouteTable.static_routes"); err != nil {
		return nil, err
	}
	return &rt, nil
}

// ScanSG — row-scanner для SecurityGroupRecord.
func ScanSG(row Scannable) (*kachorepo.SecurityGroupRecord, error) {
	var sg kachorepo.SecurityGroupRecord
	var labelsJSON []byte
	var rulesJSON []byte
	var networkID *string // nullable (kacho-proto#8: unbound / folder-level SG)
	var name, description, statusStr string

	err := row.Scan(
		&sg.ID, &sg.FolderID, &networkID, &sg.CreatedAt, &name, &description, &labelsJSON, &statusStr, &sg.DefaultForNetwork, &rulesJSON,
	)
	if err != nil {
		return nil, err
	}
	if networkID != nil {
		sg.NetworkID = *networkID
	}
	sg.Name = domain.RcNameVPC(name)
	sg.Description = domain.RcDescription(description)
	sg.Status = domain.SecurityGroupStatus(statusStr)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "SecurityGroup.labels"); err != nil {
		return nil, err
	}
	sg.Labels = domain.LabelsFromMap(labels)
	if err := UnmarshalJSONB(rulesJSON, &sg.Rules, "SecurityGroup.rules"); err != nil {
		return nil, err
	}
	return &sg, nil
}

// ScanGateway — row-scanner для GatewayRecord.
func ScanGateway(row Scannable) (*kachorepo.GatewayRecord, error) {
	var g kachorepo.GatewayRecord
	var labelsJSON []byte
	var name, description, gatewayType string

	err := row.Scan(
		&g.ID, &g.FolderID, &g.CreatedAt, &name, &description, &labelsJSON,
		&gatewayType,
	)
	if err != nil {
		return nil, err
	}
	g.Name = domain.RcNameVPC(name)
	g.Description = domain.RcDescription(description)
	g.GatewayType = domain.GatewayType(gatewayType)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "Gateway.labels"); err != nil {
		return nil, err
	}
	g.Labels = domain.LabelsFromMap(labels)
	return &g, nil
}

// ScanPrivateEndpoint — row-scanner для PrivateEndpointRecord.
func ScanPrivateEndpoint(row Scannable) (*kachorepo.PrivateEndpointRecord, error) {
	var pe kachorepo.PrivateEndpointRecord
	var labelsJSON, dnsJSON []byte
	var networkID, subnetID, addressID, ipAddress, serviceType *string
	var name, description, statusStr string

	err := row.Scan(
		&pe.ID, &pe.FolderID, &pe.CreatedAt, &name, &description, &labelsJSON,
		&networkID, &subnetID, &addressID, &ipAddress, &serviceType, &dnsJSON, &statusStr,
	)
	if err != nil {
		return nil, err
	}
	pe.Name = domain.RcNameVPC(name)
	pe.Description = domain.RcDescription(description)
	pe.Status = domain.PrivateEndpointStatus(statusStr)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "PrivateEndpoint.labels"); err != nil {
		return nil, err
	}
	pe.Labels = domain.LabelsFromMap(labels)
	if err := UnmarshalJSONB(dnsJSON, &pe.DnsOptions, "PrivateEndpoint.dns_options"); err != nil {
		return nil, err
	}
	if networkID != nil {
		pe.NetworkID = *networkID
	}
	if subnetID != nil {
		pe.SubnetID = *subnetID
	}
	if addressID != nil {
		pe.AddressID = *addressID
	}
	if ipAddress != nil {
		pe.IPAddress = *ipAddress
	}
	if serviceType != nil {
		pe.ServiceType = domain.PrivateEndpointServiceType(*serviceType)
	}
	return &pe, nil
}

// ScanNI — row-scanner для NetworkInterfaceRecord.
func ScanNI(row Scannable) (*kachorepo.NetworkInterfaceRecord, error) {
	var rec kachorepo.NetworkInterfaceRecord
	var labelsJSON, sgJSON, v4IDsJSON, v6IDsJSON []byte
	var statusName, name, description string
	if err := row.Scan(
		&rec.ID, &rec.FolderID, &rec.CreatedAt, &name, &description, &labelsJSON, &rec.SubnetID,
		&v4IDsJSON, &v6IDsJSON, &sgJSON, &rec.UsedByType, &rec.UsedByID, &rec.UsedByName, &rec.MAC, &statusName,
	); err != nil {
		return nil, err
	}
	rec.Name = domain.RcNameVPC(name)
	rec.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "NetworkInterface.labels"); err != nil {
		return nil, err
	}
	rec.Labels = domain.LabelsFromMap(labels)
	if err := UnmarshalJSONB(v4IDsJSON, &rec.V4AddressIDs, "NetworkInterface.v4_address_ids"); err != nil {
		return nil, err
	}
	if err := UnmarshalJSONB(v6IDsJSON, &rec.V6AddressIDs, "NetworkInterface.v6_address_ids"); err != nil {
		return nil, err
	}
	if err := UnmarshalJSONB(sgJSON, &rec.SecurityGroupIDs, "NetworkInterface.security_group_ids"); err != nil {
		return nil, err
	}
	rec.Status = NIStatusFromName(statusName)
	return &rec, nil
}

// ScanAddressPool — row-scanner для domain.AddressPool. Принимает pgx.Row
// (Repo использует QueryRow с pgxpool — pgx.Row API), не Scannable interface.
func ScanAddressPool(row pgx.Row) (*domain.AddressPool, error) {
	var (
		p            domain.AddressPool
		labelsJSON   []byte
		selectorJSON []byte
		kindByte     int16
		zoneIDPtr    *string
	)
	err := row.Scan(
		&p.ID, &p.Name, &p.Description, &labelsJSON,
		&p.V4CIDRBlocks, &p.V6CIDRBlocks, &kindByte, &zoneIDPtr, &p.IsDefault,
		&selectorJSON, &p.SelectorPriority, &p.CreatedAt, &p.ModifiedAt,
	)
	if err != nil {
		return nil, err
	}
	if zoneIDPtr != nil {
		p.ZoneID = *zoneIDPtr
	}
	p.Kind = domain.AddressPoolKind(kindByte)
	if err := UnmarshalJSONB(labelsJSON, &p.Labels, "address_pools.labels"); err != nil {
		return nil, err
	}
	if err := UnmarshalJSONB(selectorJSON, &p.SelectorLabels, "address_pools.selector_labels"); err != nil {
		return nil, err
	}
	return &p, nil
}
