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
	ID          string
	FolderID    string
	CreatedAt   time.Time
	Name        string
	Description string
	Labels      map[string]string
	SubnetID    string
	NetworkID   string
	// V4AddressIDs / V6AddressIDs — NIC ссылается на Address-ресурсы (kacho-vpc)
	// по id (epic KAC-2 / KAC-7). Один Address — максимум на одном NIC (enforced
	// сервис-слоем через addresses.used + referrer-tracking, см. service слой).
	V4AddressIDs []string
	V6AddressIDs []string
	// V4Addresses / V6Addresses — РЕЗОЛВЛЕННЫЕ IP-строки (denorm, output-only).
	// Заполняются сервис-слоем в internal-проекции (InternalNetworkInterfaceService.
	// Get/ListByHypervisor) из соответствующих Address-ресурсов; в публичном NIC API
	// не surface'ятся (там только v4_address_ids/v6_address_ids — workspace CLAUDE.md
	// §«Инфра-чувствительные данные» — резолвленные IP уходят только в data-plane).
	V4Addresses      []string
	V6Addresses      []string
	SecurityGroupIDs []string
	InstanceID       string
	Index            string
	Status           NetworkInterfaceStatus
	Dataplane        NICDataplane
}
