// cockroach.go — scaffold-реализация [Dialect] для CockroachDB
// (skill evgeniy §9 K.3 — "migrator multi-dialect-ready").
//
// # Status: experimental scaffold
//
// CockroachDB совместим с PostgreSQL wire protocol — поэтому базовый apply
// миграций работает через тот же goose + pgx driver (с goose-dialect="postgres";
// см. [SpecCockroach]). Эта реализация переиспользует общие helpers
// [openPgxDB] / [setupGoose] из `postgres.go`.
//
// # Production caveats
//
// Текущие kacho-vpc-миграции НЕ полностью совместимы с CockroachDB:
//
//   - EXCLUDE USING gist (CIDR-overlap у subnets — `0001_initial.sql`,
//     constraint `subnets_no_overlap_v4` / `_v6`) — CockroachDB не
//     поддерживает gist-индексы и EXCLUDE-constraints вовсе. Миграцию
//     придётся переписать через триггерную проверку либо альтернативную
//     CRDB-конструкцию (computed CIDR-range columns + index).
//
//   - pg_notify / LISTEN/NOTIFY (outbox trigger `vpc_outbox_notify_trg` +
//     `InternalWatchHandler.Watch` через dedicated pgx-conn). CockroachDB
//     поддерживает CHANGEFEED, но семантика отличается — InternalWatchHandler
//     придётся переписать под CHANGEFEED INTO sink.
//
//   - xmin::text OCC (security_group_repo.UpdateRules через системную колонку
//     `xmin` — см. `internal/repo/security_group_repo.go`). CockroachDB
//     использует HLC timestamps вместо MVCC-xid; xmin недоступен. Альтернатива —
//     явная колонка `revision` BIGINT + CAS на UPDATE.
//
//   - btree_gist extension (`CREATE EXTENSION btree_gist`) — нужен для
//     postgres EXCLUDE constraints; в CRDB этот extension не существует.
//
// Это scaffold для future "kacho-vpc-cockroach-port" эпика — production-readiness
// требует параллельные cockroach-compat миграции (или wrap `migrate.go` так,
// чтобы конкретные DDL-выражения подменялись per-dialect). Текущее состояние:
//
//   - `kacho-migrator --dialect cockroach status` против пустой CRDB — работает
//     (создаёт `goose_db_version`, идёт по списку).
//   - `kacho-migrator --dialect cockroach up` — упадёт на первой же миграции
//     с EXCLUDE USING gist (0001_initial.sql), что и есть желаемое UX
//     ("этот dialect не готов") вместо silent-corruption.
//
// Integration-тестов под реальный CockroachDB-контейнер пока НЕТ — добавлять
// под отдельный тикет, когда production-need будет.
package migrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// cockroachDialect — scaffold-реализация [Dialect] для CockroachDB.
// Делегирует goose тот же путь, что postgres (через pgx driver и
// goose-dialect="postgres"); различия — только в Spec() и в комментариях
// выше для production-readiness.
type cockroachDialect struct{}

// newCockroachDialect создаёт cockroachDialect.
func newCockroachDialect() *cockroachDialect { return &cockroachDialect{} }

// Spec возвращает DialectSpec CockroachDB.
func (c *cockroachDialect) Spec() DialectSpec { return SpecCockroach }

// Up применяет миграции (до target-версии либо все) через goose.
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

// Down откатывает миграции (до target-версии либо на шаг назад) через goose.
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

// Status печатает applied/pending миграции через goose.
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

// Create создаёт новый пустой SQL-файл миграции на диске (без подключения к БД).
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
