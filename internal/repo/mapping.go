package repo

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

func uuidToStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

func strToUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

func tsToTime(ts pgtype.Timestamptz) time.Time {
	if ts.Valid {
		return ts.Time
	}
	return time.Time{}
}

func tsToTimePtr(ts pgtype.Timestamptz) *time.Time {
	if ts.Valid {
		t := ts.Time
		return &t
	}
	return nil
}

func jsonbToMap(b []byte) map[string]string {
	if len(b) == 0 {
		return map[string]string{}
	}
	m := map[string]string{}
	_ = json.Unmarshal(b, &m)
	return m
}

func mapToJSONB(m map[string]string) []byte {
	if len(m) == 0 {
		b, _ := json.Marshal(map[string]string{})
		return b
	}
	b, _ := json.Marshal(m)
	return b
}

// networkSpecJSON хранит данные spec для Network.
type networkSpecJSON struct {
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

// networkStatusJSON хранит данные status для Network.
type networkStatusJSON struct {
	State string `json:"state"`
}

func domainNetworkToSpec(n *domain.Network) []byte {
	b, _ := json.Marshal(networkSpecJSON{DisplayName: n.DisplayName, Description: n.Description})
	return b
}

func domainNetworkToStatus(n *domain.Network) []byte {
	state := n.State
	if state == "" {
		state = "ACTIVE"
	}
	b, _ := json.Marshal(networkStatusJSON{State: state})
	return b
}

// subnetSpecJSON хранит данные spec для Subnet.
type subnetSpecJSON struct {
	NetworkID   string `json:"network_id,omitempty"`
	CIDRBlock   string `json:"cidr_block,omitempty"`
	ZoneID      string `json:"zone_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type subnetStatusJSON struct {
	State string `json:"state"`
}

func domainSubnetToSpec(s *domain.Subnet) []byte {
	b, _ := json.Marshal(subnetSpecJSON{
		NetworkID:   s.NetworkID,
		CIDRBlock:   s.CIDRBlock,
		ZoneID:      s.ZoneID,
		DisplayName: s.DisplayName,
		Description: s.Description,
	})
	return b
}

func domainSubnetToStatus(s *domain.Subnet) []byte {
	state := s.State
	if state == "" {
		state = "ACTIVE"
	}
	b, _ := json.Marshal(subnetStatusJSON{State: state})
	return b
}

// sgSpecJSON хранит данные spec для SecurityGroup.
type sgSpecJSON struct {
	NetworkID   string                   `json:"network_id,omitempty"`
	DisplayName string                   `json:"display_name,omitempty"`
	Description string                   `json:"description,omitempty"`
	Rules       []sgRuleJSON             `json:"rules,omitempty"`
}

type sgRuleJSON struct {
	ID           string   `json:"id"`
	Direction    string   `json:"direction"`
	Protocol     string   `json:"protocol"`
	PortRangeMin int32    `json:"port_range_min,omitempty"`
	PortRangeMax int32    `json:"port_range_max,omitempty"`
	CIDRBlocks   []string `json:"cidr_blocks,omitempty"`
	Description  string   `json:"description,omitempty"`
}

type sgStatusJSON struct {
	State string `json:"state"`
}

func domainSGToSpec(sg *domain.SecurityGroup) []byte {
	rules := make([]sgRuleJSON, len(sg.Rules))
	for i, r := range sg.Rules {
		rules[i] = sgRuleJSON{
			ID:           r.ID,
			Direction:    r.Direction,
			Protocol:     r.Protocol,
			PortRangeMin: r.PortRangeMin,
			PortRangeMax: r.PortRangeMax,
			CIDRBlocks:   r.CIDRBlocks,
			Description:  r.Description,
		}
	}
	b, _ := json.Marshal(sgSpecJSON{
		NetworkID:   sg.NetworkID,
		DisplayName: sg.DisplayName,
		Description: sg.Description,
		Rules:       rules,
	})
	return b
}

func domainSGToStatus(sg *domain.SecurityGroup) []byte {
	state := sg.State
	if state == "" {
		state = "ACTIVE"
	}
	b, _ := json.Marshal(sgStatusJSON{State: state})
	return b
}

func specToSGRules(spec []byte) []domain.SecurityGroupRule {
	var s sgSpecJSON
	if err := json.Unmarshal(spec, &s); err != nil {
		return nil
	}
	rules := make([]domain.SecurityGroupRule, len(s.Rules))
	for i, r := range s.Rules {
		rules[i] = domain.SecurityGroupRule{
			ID:           r.ID,
			Direction:    r.Direction,
			Protocol:     r.Protocol,
			PortRangeMin: r.PortRangeMin,
			PortRangeMax: r.PortRangeMax,
			CIDRBlocks:   r.CIDRBlocks,
			Description:  r.Description,
		}
	}
	return rules
}

// rtSpecJSON хранит данные spec для RouteTable.
type rtSpecJSON struct {
	NetworkID    string          `json:"network_id,omitempty"`
	DisplayName  string          `json:"display_name,omitempty"`
	Description  string          `json:"description,omitempty"`
	StaticRoutes []staticRouteJSON `json:"static_routes,omitempty"`
}

type staticRouteJSON struct {
	ID                 string `json:"id"`
	DestinationPrefix  string `json:"destination_prefix"`
	NextHopAddress     string `json:"next_hop_address"`
	Description        string `json:"description,omitempty"`
}

type rtStatusJSON struct {
	State string `json:"state"`
}

func domainRTToSpec(rt *domain.RouteTable) []byte {
	routes := make([]staticRouteJSON, len(rt.StaticRoutes))
	for i, r := range rt.StaticRoutes {
		routes[i] = staticRouteJSON{
			ID:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}
	b, _ := json.Marshal(rtSpecJSON{
		NetworkID:    rt.NetworkID,
		DisplayName:  rt.DisplayName,
		Description:  rt.Description,
		StaticRoutes: routes,
	})
	return b
}

func domainRTToStatus(rt *domain.RouteTable) []byte {
	state := rt.State
	if state == "" {
		state = "ACTIVE"
	}
	b, _ := json.Marshal(rtStatusJSON{State: state})
	return b
}

func specToStaticRoutes(spec []byte) []domain.StaticRoute {
	var s rtSpecJSON
	if err := json.Unmarshal(spec, &s); err != nil {
		return nil
	}
	routes := make([]domain.StaticRoute, len(s.StaticRoutes))
	for i, r := range s.StaticRoutes {
		routes[i] = domain.StaticRoute{
			ID:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}
	return routes
}

// addrSpecJSON хранит данные spec для Address.
type addrSpecJSON struct {
	AddressType string `json:"address_type,omitempty"`
	ZoneID      string `json:"zone_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type addrStatusJSON struct {
	State         string `json:"state"`
	AllocatedIPv4 string `json:"allocated_ipv4,omitempty"`
}

func domainAddrToSpec(a *domain.Address) []byte {
	b, _ := json.Marshal(addrSpecJSON{
		AddressType: a.AddressType,
		ZoneID:      a.ZoneID,
		DisplayName: a.DisplayName,
		Description: a.Description,
	})
	return b
}

func domainAddrToStatus(a *domain.Address) []byte {
	state := a.State
	if state == "" {
		state = "RESERVED"
	}
	b, _ := json.Marshal(addrStatusJSON{State: state, AllocatedIPv4: a.AllocatedIPv4})
	return b
}
