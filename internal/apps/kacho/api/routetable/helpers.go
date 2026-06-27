// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package routetable

import (
	"fmt"
	"net/netip"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	// Blank-import регистрирует трансферы RouteTable/time через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// marshalRouteTableRecord конвертирует repo-entity RouteTable в *anypb.Any
// через DTO-реестр.
func marshalRouteTableRecord(rec *kacho.RouteTableRecord) (*anypb.Any, error) {
	var dst *vpcv1.RouteTable
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer RouteTable: %w", err)
	}
	return anypb.New(dst)
}

// validateStaticRoutes проверяет каждую запись routes:
//   - destinationPrefix: валидный CIDR (IPv4 или IPv6) без host-bits;
//   - nextHopAddress: валидный IP-адрес (IPv4 или IPv6).
//
// Пустой массив — допустим (route table без статических маршрутов).
// При нарушении — InvalidArgument с FieldViolation `static_routes[<i>].<field>`.
func validateStaticRoutes(routes []domain.StaticRoute) error {
	for i, r := range routes {
		dpField := fmt.Sprintf("static_routes[%d].destination_prefix", i)
		if r.DestinationPrefix == "" {
			return serviceerr.InvalidArg(dpField, dpField+" is required")
		}
		prefix, err := netip.ParsePrefix(r.DestinationPrefix)
		if err != nil {
			return serviceerr.InvalidArg(dpField, dpField+" must be a valid CIDR (e.g. 10.0.0.0/24)")
		}
		if prefix.Masked() != prefix {
			return serviceerr.InvalidArg(dpField,
				dpField+" must have zero host-bits (use the network address "+prefix.Masked().String()+")")
		}
		nhField := fmt.Sprintf("static_routes[%d].next_hop_address", i)
		if r.NextHopAddress == "" {
			return serviceerr.InvalidArg(nhField, nhField+" is required")
		}
		if _, err := netip.ParseAddr(r.NextHopAddress); err != nil {
			return serviceerr.InvalidArg(nhField, nhField+" must be a valid IP address (IPv4 or IPv6)")
		}
	}
	return nil
}
