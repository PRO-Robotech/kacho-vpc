package domain

import "time"

// NetworkInterfaceStatus — грубый статус NIC (зеркалит vpcv1.NetworkInterface_Status).
type NetworkInterfaceStatus int

// Значения NetworkInterfaceStatus.
const (
	NIStatusUnspecified NetworkInterfaceStatus = iota
	NIStatusProvisioning
	NIStatusActive
	NIStatusAvailable
	NIStatusFailed
	NIStatusDeleting
)

// NICDataplane — internal data-plane-проекция NIC (заполняется kacho-vpc-implement
// через ReportNiDataplane). ИНФРА-ЧУВСТВИТЕЛЬНОЕ — не на публичной поверхности.
type NICDataplane struct {
	HVID        string
	SID         string
	SIDSeq      uint32
	HostIface   string
	Netns       string
	GatewayIP   string
	ContainerID string
	StatusError string
	Revision    uint64
	UpdatedAt   *time.Time
}

// NetworkInterface — first-class сетевой интерфейс (AWS-ENI-style).
type NetworkInterface struct {
	ID               string
	FolderID         string
	CreatedAt        time.Time
	Name             string
	Description      string
	Labels           map[string]string
	SubnetID         string
	NetworkID        string
	PrimaryV4Address string
	// SecondaryV4Addresses / V6Addresses — у NIC может быть несколько IPv4/IPv6
	// (AWS-ENI-style): один primary IPv4 + secondary IPv4 + IPv6-адреса.
	SecondaryV4Addresses []string
	V6Addresses          []string
	SecurityGroupIDs     []string
	InstanceID           string
	Index                string
	Status               NetworkInterfaceStatus
	Dataplane            NICDataplane
}
