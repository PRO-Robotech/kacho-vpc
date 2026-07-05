// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package gateway

import (
	"fmt"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	// Blank-import регистрирует трансферы Gateway/time через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
)

// marshalGatewayRecord конвертирует repo-entity Gateway в *anypb.Any через
// DTO-реестр. Используется worker'ами для упаковки результата в Operation.response.
func marshalGatewayRecord(rec *kacho.GatewayRecord) (*anypb.Any, error) {
	var dst *vpcv1.Gateway
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Gateway: %w", err)
	}
	return anypb.New(dst)
}
