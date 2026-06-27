// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
// default_security_group_id nullable (FK ON DELETE SET NULL) — читаем через
// COALESCE(..., ”) чтобы NULL → "" (proto-контракт не меняется, ScanNetwork
// сканирует в string).
const NetworkCols = `id, project_id, created_at, name, description, labels, COALESCE(default_security_group_id, '') AS default_security_group_id, COALESCE(vrf_id, 0) AS vrf_id`

// SubnetCols — список колонок таблицы subnets в порядке, ожидаемом ScanSubnet.
const SubnetCols = `id, project_id, created_at, name, description, labels, network_id, zone_id, v4_cidr_blocks, v6_cidr_blocks, route_table_id, dhcp_options`

// AddressCols — список колонок таблицы addresses в порядке, ожидаемом ScanAddress.
const AddressCols = `id, project_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4, internal_ipv6, external_ipv6`

// RouteTableCols — список колонок таблицы route_tables в порядке, ожидаемом ScanRouteTable.
const RouteTableCols = `id, project_id, created_at, name, description, labels, network_id, static_routes`

// SGCols — список колонок таблицы security_groups в порядке, ожидаемом ScanSG.
// Колонки `status` нет: она удалена из контракта (DROP COLUMN в миграции 0003).
const SGCols = `id, project_id, network_id, created_at, name, description, labels, default_for_network, rules`

// GatewayCols — список колонок таблицы gateways в порядке, ожидаемом ScanGateway.
const GatewayCols = `id, project_id, created_at, name, description, labels, gateway_type`

// NICCols — список колонок таблицы network_interfaces в порядке, ожидаемом ScanNI.
const NICCols = `id, project_id, created_at, name, description, labels, subnet_id,
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
	var vrf int64

	err := row.Scan(
		&n.ID, &n.ProjectID, &n.CreatedAt, &name, &description, &labelsJSON,
		&n.DefaultSecurityGroupID, &vrf,
	)
	if err != nil {
		return nil, err
	}
	n.VRFID = uint32(vrf)
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
		&s.ID, &s.ProjectID, &s.CreatedAt, &name, &description, &labelsJSON,
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
		&a.ID, &a.ProjectID, &a.CreatedAt, &name, &description, &labelsJSON,
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
		&rt.ID, &rt.ProjectID, &rt.CreatedAt, &name, &description, &labelsJSON,
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
	var networkID *string // nullable: unbound / project-level SG
	var name, description string

	err := row.Scan(
		&sg.ID, &sg.ProjectID, &networkID, &sg.CreatedAt, &name, &description, &labelsJSON, &sg.DefaultForNetwork, &rulesJSON,
	)
	if err != nil {
		return nil, err
	}
	if networkID != nil {
		sg.NetworkID = *networkID
	}
	sg.Name = domain.RcNameVPC(name)
	sg.Description = domain.RcDescription(description)
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
		&g.ID, &g.ProjectID, &g.CreatedAt, &name, &description, &labelsJSON,
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

// ScanNI — row-scanner для NetworkInterfaceRecord.
func ScanNI(row Scannable) (*kachorepo.NetworkInterfaceRecord, error) {
	var rec kachorepo.NetworkInterfaceRecord
	var labelsJSON, sgJSON, v4IDsJSON, v6IDsJSON []byte
	var statusName, name, description string
	if err := row.Scan(
		&rec.ID, &rec.ProjectID, &rec.CreatedAt, &name, &description, &labelsJSON, &rec.SubnetID,
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

// ScanAddressPool — row-scanner для AddressPoolRecord. Принимает pgx.Row
// (Repo использует QueryRow с pgxpool — pgx.Row API). created_at/modified_at
// сканируются в собственные поля Record (DB-managed timestamps), domain-поля —
// в embed'нутый domain.AddressPool; name/description/labels конвертируются в
// self-validating newtypes (паритет с ScanSubnet).
func ScanAddressPool(row pgx.Row) (*kachorepo.AddressPoolRecord, error) {
	var (
		rec          kachorepo.AddressPoolRecord
		name         string
		description  string
		labelsJSON   []byte
		selectorJSON []byte
		kindByte     int16
		zoneIDPtr    *string
	)
	err := row.Scan(
		&rec.ID, &name, &description, &labelsJSON,
		&rec.V4CIDRBlocks, &rec.V6CIDRBlocks, &kindByte, &zoneIDPtr, &rec.IsDefault,
		&selectorJSON, &rec.SelectorPriority, &rec.CreatedAt, &rec.ModifiedAt,
	)
	if err != nil {
		return nil, err
	}
	rec.Name = domain.RcNameVPC(name)
	rec.Description = domain.RcDescription(description)
	if zoneIDPtr != nil {
		rec.ZoneID = *zoneIDPtr
	}
	rec.Kind = domain.AddressPoolKind(kindByte)
	var labels, selector map[string]string
	if err := UnmarshalJSONB(labelsJSON, &labels, "address_pools.labels"); err != nil {
		return nil, err
	}
	if err := UnmarshalJSONB(selectorJSON, &selector, "address_pools.selector_labels"); err != nil {
		return nil, err
	}
	rec.Labels = domain.LabelsFromMap(labels)
	rec.SelectorLabels = domain.LabelsFromMap(selector)
	return &rec, nil
}
