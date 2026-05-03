// Package migrations содержит embedded SQL-миграции сервиса kacho-vpc.
package migrations

import "embed"

// FS — embedded файловая система с goose-миграциями.
//
//go:embed *.sql
var FS embed.FS
