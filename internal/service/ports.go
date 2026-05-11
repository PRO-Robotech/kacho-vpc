package service

import "github.com/PRO-Robotech/kacho-vpc/internal/ports"

// Port-интерфейсы и связанные value-объекты вынесены в leaf-пакет
// `internal/ports` (см. TODO #12) — это позволяет переиспользовать общий
// test-helper `internal/ports/portmock` без import-cycle. Здесь — type-alias'ы
// для обратной совместимости: существующий код (`service.NetworkRepo`,
// `service.Pagination`, ...) и adapter'ы (`internal/repo`, `internal/clients`)
// продолжают ссылаться на `service.*` именах.

type (
	// Pagination — постраничная навигация.
	Pagination = ports.Pagination

	// NetworkFilter — фильтр для списка сетей.
	NetworkFilter = ports.NetworkFilter
	// SubnetFilter — фильтр для списка подсетей.
	SubnetFilter = ports.SubnetFilter
	// AddressFilter — фильтр для списка адресов.
	AddressFilter = ports.AddressFilter
	// RouteTableFilter — фильтр для списка таблиц маршрутизации.
	RouteTableFilter = ports.RouteTableFilter
	// SecurityGroupFilter — фильтр для списка SG.
	SecurityGroupFilter = ports.SecurityGroupFilter
	// GatewayFilter — фильтр для списка NAT Gateways.
	GatewayFilter = ports.GatewayFilter
	// PrivateEndpointFilter — фильтр для списка PrivateEndpoints.
	PrivateEndpointFilter = ports.PrivateEndpointFilter
	// AddressPoolFilter — фильтр для списка пулов.
	AddressPoolFilter = ports.AddressPoolFilter

	// NetworkRepo — port-интерфейс репозитория сетей.
	NetworkRepo = ports.NetworkRepo
	// SubnetRepo — port-интерфейс репозитория подсетей.
	SubnetRepo = ports.SubnetRepo
	// AddressRepo — port-интерфейс репозитория адресов.
	AddressRepo = ports.AddressRepo
	// SecurityGroupRepo — port-интерфейс репозитория SG.
	SecurityGroupRepo = ports.SecurityGroupRepo
	// GatewayRepo — port-интерфейс репозитория Gateways.
	GatewayRepo = ports.GatewayRepo
	// PrivateEndpointRepo — port-интерфейс репозитория PrivateEndpoints.
	PrivateEndpointRepo = ports.PrivateEndpointRepo
	// RouteTableRepo — port-интерфейс репозитория таблиц маршрутизации.
	RouteTableRepo = ports.RouteTableRepo
	// AddressPoolRepo — port-интерфейс репозитория пулов адресов.
	AddressPoolRepo = ports.AddressPoolRepo
	// AddressPoolBindingRepo — explicit биндинги pool ↔ network/address.
	AddressPoolBindingRepo = ports.AddressPoolBindingRepo
	// CloudPoolSelectorRepo — admin-controlled routing-labels for Cloud.
	CloudPoolSelectorRepo = ports.CloudPoolSelectorRepo

	// FolderClient — port для проверки существования Folder и lookup'а cloud_id.
	FolderClient = ports.FolderClient
	// SubnetExistsChecker — port для проверки существования Subnet.
	SubnetExistsChecker = ports.SubnetExistsChecker
	// ZoneRegistry — port для проверки существования зоны (таблица `zones`).
	ZoneRegistry = ports.ZoneRegistry
)
