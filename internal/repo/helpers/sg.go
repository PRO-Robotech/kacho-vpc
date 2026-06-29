// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WrapSGErr — обертка над WrapPgErr со специфичным для SecurityGroup not-found
// текстом ("Security group SecurityGroup.Id(value=%s) not found" — часть
// контракта). Остальные классы ошибок — через WrapPgErr.
func WrapSGErr(err error, id string) error {
	if errors.Is(err, pgx.ErrNoRows) && id != "" {
		return fmt.Errorf("%w: Security group SecurityGroup.Id(value=%s) not found", ErrNotFound, id)
	}
	return WrapPgErr(err, "SecurityGroup", id)
}

// WrapGatewayErr — обертка над WrapPgErr с kind="Gateway"
// (parity с WrapPgErr для Network/Subnet/...).
func WrapGatewayErr(err error, id string) error {
	return WrapPgErr(err, "Gateway", id)
}
