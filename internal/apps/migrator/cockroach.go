// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// cockroach.go — реализация [Dialect] для CockroachDB.
//
// CockroachDB совместим с PostgreSQL wire protocol, поэтому базовый apply
// миграций идет через тот же goose + pgx driver (goose-dialect="postgres",
// см. [SpecCockroach]); общие helpers [openPgxDB] / [setupGoose] берутся из
// postgres.go.
//
// # Ограничения совместимости
//
// Текущие миграции kacho-vpc не полностью совместимы с CockroachDB:
//
//   - EXCLUDE USING gist (CIDR-overlap у subnets — `0001_initial.sql`,
//     constraint `subnets_no_overlap_v4` / `_v6`): CockroachDB не поддерживает
//     gist-индексы и EXCLUDE-constraints. Понадобится переписать через
//     триггерную проверку или computed CIDR-range columns + index.
//
//   - pg_notify / LISTEN/NOTIFY (outbox trigger `vpc_outbox_notify_trg` +
//     register-drainer/reconciler через dedicated pgx-conn; доставка изменений
//     клиентам — polling Operation.Get/List): семантика CockroachDB CHANGEFEED
//     отличается — notify-механизм придется переписать под CHANGEFEED INTO sink.
//
//   - xmin::text OCC (security_group_repo.UpdateRules через системную колонку
//     `xmin`, см. `internal/repo/security_group_repo.go`): CockroachDB
//     использует HLC timestamps вместо MVCC-xid, xmin недоступен. Альтернатива —
//     явная колонка `revision` BIGINT + CAS на UPDATE.
//
//   - btree_gist extension (`CREATE EXTENSION btree_gist`): нужен для postgres
//     EXCLUDE constraints, в CockroachDB не существует.
//
// Поэтому фактическое поведение сейчас такое:
//
//   - `kacho-migrator --dialect cockroach status` против пустой БД — работает
//     (создает `goose_db_version`, идет по списку).
//   - `kacho-migrator --dialect cockroach up` — падает на первой же миграции с
//     EXCLUDE USING gist (`0001_initial.sql`); это и есть нужный UX «диалект не
//     готов» вместо тихого повреждения данных.
package migrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// cockroachDialect — реализация [Dialect] для CockroachDB. Делегирует goose тот
// же путь, что postgres (pgx driver + goose-dialect="postgres"); отличие — только
// в Spec(). Известные ограничения совместимости описаны в шапке файла.
type cockroachDialect struct{}

func newCockroachDialect() *cockroachDialect { return &cockroachDialect{} }

func (c *cockroachDialect) Spec() DialectSpec { return SpecCockroach }

func (c *cockroachDialect) Up(ctx context.Context, dsn string, fsys fs.FS, dir string, target string) error {
	db, err := openPgxDB(dsn, c.Spec())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := setupGoose(fsys, c.Spec()); err != nil {
		return err
	}
	if target == "" {
		return goose.UpContext(ctx, db, dir)
	}
	version, perr := parseTargetVersion(target)
	if perr != nil {
		return perr
	}
	return goose.UpToContext(ctx, db, dir, version)
}

func (c *cockroachDialect) Down(ctx context.Context, dsn string, fsys fs.FS, dir string, target string) error {
	db, err := openPgxDB(dsn, c.Spec())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := setupGoose(fsys, c.Spec()); err != nil {
		return err
	}
	if target == "" {
		return goose.DownContext(ctx, db, dir)
	}
	version, perr := parseTargetVersion(target)
	if perr != nil {
		return perr
	}
	return goose.DownToContext(ctx, db, dir, version)
}

func (c *cockroachDialect) Status(ctx context.Context, dsn string, fsys fs.FS, dir string, out io.Writer) error {
	db, err := openPgxDB(dsn, c.Spec())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := setupGoose(fsys, c.Spec()); err != nil {
		return err
	}
	_ = out // goose v3 пишет в свой logger
	return goose.StatusContext(ctx, db, dir)
}

func (c *cockroachDialect) Create(physDir, name string) error {
	if name == "" {
		return errors.New("migration name is empty")
	}
	if physDir == "" {
		return errors.New("physical migrations directory is empty (--dir)")
	}
	if err := goose.SetDialect(c.Spec().GooseDialect); err != nil {
		return fmt.Errorf("goose set dialect %q: %w", c.Spec().GooseDialect, err)
	}
	return goose.Create(nil, physDir, name, "sql")
}
