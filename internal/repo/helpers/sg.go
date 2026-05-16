package helpers

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WrapSGErr — обёртка над WrapPgErr с verbatim-YC SG-specific not-found
// текстом ("Security group SecurityGroup.Id(value=%s) not found", probe
// 2026-05-11, kacho-vpc#10). Остальные классы ошибок — через WrapPgErr.
func WrapSGErr(err error, id string) error {
	if errors.Is(err, pgx.ErrNoRows) && id != "" {
		return fmt.Errorf("%w: Security group SecurityGroup.Id(value=%s) not found", ErrNotFound, id)
	}
	return WrapPgErr(err, "SecurityGroup", id)
}

// WrapGatewayErr — обёртка над WrapPgErr со значением kind="Gateway"
// (parity с WrapPgErr для Network/Subnet/...).
func WrapGatewayErr(err error, id string) error {
	return WrapPgErr(err, "Gateway", id)
}
