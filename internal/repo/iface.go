// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// iface.go — value-объекты (Pagination, *Filter) и peer-service port-интерфейсы
// (ProjectClient / ZoneRegistry / SubnetExistsChecker) для use-case слоя kacho-vpc.
//
// Use-case-слой VPC, admin-services и peer-port'ы работают через CQRS-Repository
// (`internal/repo/kacho`) — `kacho.Repository` с разделением `Reader(ctx)` /
// `Writer(ctx)`. Узкие port'ы admin/peer-сервисов получают тонкие adapter'ы
// поверх `kacho.Repository` из пакета `internal/repo/cqrsadapter`.
//
// Здесь живут только Filter-type-alias'ы (`SubnetFilter` / `NetworkFilter` /
// `AddressFilter` / …), проксирующие на leaf-пакет `internal/repo/kacho`, и
// peer-service port'ы (ProjectClient / SubnetExistsChecker / ZoneRegistry).

package repo

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination — постраничная навигация. Живет в leaf-пакете
// `internal/repo/kacho/` (вместе с NetworkFilter / SecurityGroupFilter /
// NetworkRecord), чтобы избежать import-cycle `repo → repo/kacho → repo`. Здесь —
// type-alias для всех callers (`internal/apps/kacho/api/*/iface.go`, `repomock`,
// integration-тесты).
type Pagination = kachorepo.Pagination

// NetworkFilter — фильтр для списка сетей.
//
// Name — точное совпадение имени (для sync uniqueness-check в Create; без парсинга
// filter-выражения).
//
// type-alias на `kacho.NetworkFilter` — см. doc на Pagination выше.
type NetworkFilter = kachorepo.NetworkFilter

// SubnetFilter — фильтр для списка подсетей.
//
// type-alias на `kacho.SubnetFilter` — см. doc на Pagination/NetworkFilter выше.
type SubnetFilter = kachorepo.SubnetFilter

// AddressFilter — фильтр для списка адресов.
//
// type-alias на `kacho.AddressFilter`: Address use-cases ходят в
// `kacho.AddressReaderIface.List(ctx, f kacho.AddressFilter, ...)`, поэтому фильтр
// здесь и в CQRS-iface обязан быть одним типом.
type AddressFilter = kachorepo.AddressFilter

// RouteTableFilter — фильтр для списка таблиц маршрутизации. type-alias на
// `kacho.RouteTableFilter`.
type RouteTableFilter = kachorepo.RouteTableFilter

// SecurityGroupFilter — фильтр для списка SG.
//
// type-alias на `kacho.SecurityGroupFilter` — см. doc на Pagination выше.
type SecurityGroupFilter = kachorepo.SecurityGroupFilter

// AddressPoolFilter — фильтр для списка пулов. AddressPool — глобальный
// infrastructure-ресурс, поэтому project/cloud/org здесь нет.
//
// type-alias на `kacho.AddressPoolFilter` для всех callers
// (`internal/repo/address_pool_repo.go`, `internal/apps/kacho/api/addresspool/*`).
type AddressPoolFilter = kachorepo.AddressPoolFilter

// ProjectClient — port для проверки существования Project.
type ProjectClient interface {
	Exists(ctx context.Context, projectID string) (bool, error)
}

// ZoneRegistry — port для проверки существования зоны.
//
// Используется SubnetService / AddressPoolService для валидации `zone_id` в
// `Create`. Хардкод-whitelist допустимых zone-id отсутствует: источник истины —
// leaf-домен kacho-geo (geo.v1.ZoneService.Get); реализация порта —
// internal/clients/geo_client.go.
//
// Get возвращает ErrNotFound для несуществующей зоны.
// ListIDs возвращает идентификаторы всех зарегистрированных зон — нужен для
// динамического сообщения `must be one of: ...`. Без пагинации: зон в системе
// единицы, даже при росте до десятков все помещается в один SELECT.
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
	ListIDs(ctx context.Context) ([]string, error)
}
