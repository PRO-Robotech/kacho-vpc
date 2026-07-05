// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

import (
	"fmt"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	// Blank-import регистрирует трансферы Network/time через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// marshalNetworkRecord конвертирует repo-entity Network в *anypb.Any через
// DTO-реестр. Используется worker'ами Create/Update/Move для упаковки результата
// в Operation.response.
func marshalNetworkRecord(rec *kachorepo.NetworkRecord) (*anypb.Any, error) {
	var dst *vpcv1.Network
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Network: %w", err)
	}
	return anypb.New(dst)
}
