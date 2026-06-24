// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package toproto — реализации DTO-трансферов domain/repo → proto. Каждый
// трансфер регистрируется в реестре через init(); зарегистрированы все
// VPC-ресурсы (Network/Subnet/Address/RouteTable/SecurityGroup/Gateway/
// NetworkInterface) + time.Time → *timestamppb.Timestamp.
package toproto

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
)

// timeObj — нулевой struct-receiver для метода-трансфера time.Time → pb timestamp.
// Существует ради единого стиля «<resource>{}.toPb» (см. network.go), а не
// «свободная функция» — это упрощает grep по `\bnetwork\b{}.toPb` и т.п.
type timeObj struct{}

// toPb — truncate до секунд (БД хранит микросекунды, в proto отдаем секунды).
// Nil-receiver для time.Time не имеет смысла (это value-тип, не pointer);
// «zero» time → timestamppb «zero» (1970-01-01). Caller проверяет
// `t.IsZero()` если хочет вернуть nil-pb-timestamp.
func (timeObj) toPb(t time.Time) (*timestamppb.Timestamp, error) {
	return timestamppb.New(t.Truncate(time.Second)), nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(timeObj{}.toPb))
}
