// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

import (
	"fmt"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует DTO-трансферы (включая NetworkInterface) через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// niResource — название ресурса для сообщений `corevalidate.ResourceID`.
const niResource = "network interface"

// niResourceID — sync-валидация формата NIC-id (3-char crockford-base32 prefix
// + 17-char base32). NIC имеет собственный prefix `nic`
// (`ids.PrefixNetworkInterface`). Проверка family-agnostic (corevalidate.ResourceID
// игнорирует expectedPrefix, сверяя лишь known-set), prefix передается для
// читаемости call-site.
func niResourceID(id string) error {
	return corevalidate.ResourceID(niResource, ids.PrefixNetworkInterface, id)
}

// validateNICAddressCardinality — fast-fail sync-валидация: на одной NetworkInterface
// разрешен максимум один IPv4 и максимум один IPv6. Совпадает с DB-уровнем
// `network_interfaces_v4_addr_max1` / `_v6_addr_max1` (DB-side — финальный backstop,
// эта функция дает понятный InvalidArgument до создания Operation). Multi-IP на VM
// реализуется через несколько NIC, а не через secondary-адреса в одном NIC.
func validateNICAddressCardinality(v4IDs, v6IDs []string) error {
	if len(v4IDs) > 1 {
		return serviceerr.InvalidArg("v4_address_ids", "at most one IPv4 address per network interface (use multiple NICs for multi-IP)")
	}
	if len(v6IDs) > 1 {
		return serviceerr.InvalidArg("v6_address_ids", "at most one IPv6 address per network interface (use multiple NICs for multi-IP)")
	}
	return nil
}

// marshalNetworkInterfaceRecord конвертирует repo-entity NIC в *anypb.Any через
// DTO-реестр. Используется worker'ами Create/Update для упаковки результата в
// Operation.response.
func marshalNetworkInterfaceRecord(rec *kachorepo.NetworkInterfaceRecord) (*anypb.Any, error) {
	var dst *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer NetworkInterface: %w", err)
	}
	return anypb.New(dst)
}
