// postgres.go — production-реализация [Dialect] для PostgreSQL через
// goose + pgx driver (skill evgeniy §9 K.3).
//
// До KAC-94 эта логика жила прямо в Runner; вынесена в отдельный тип, чтобы
// добавить cockroach (и любой другой диалект) без if-ветвей в Runner.
// Public-API пакета не изменился: Runner.Up/Down/Status/Create делегирует
// в Dialect-impl.
package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// postgresDialect — реализация [Dialect] для PostgreSQL.
//
// Stateless: один экземпляр на жизнь процесса безопасен, но т.к. внутри
// goose использует пакетные глобалки (`goose.SetBaseFS`, `goose.SetDialect`),
// параллельные Up/Down из одного процесса для разных диалектов не
// поддерживаются. CLI гоняет одну команду за раз — это ок.
type postgresDialect struct{}

// newPostgresDialect создаёт postgresDialect.
func newPostgresDialect() *postgresDialect { return &postgresDialect{} }

// Spec возвращает DialectSpec PostgreSQL.
func (p *postgresDialect) Spec() DialectSpec { return SpecPostgres }

// Up применяет миграции (до target-версии либо все) через goose.
func (p *postgresDialect) Up(ctx context.Context, dsn string, fsys fs.FS, dir string, target string) error {
	db, err := openPgxDB(dsn, p.Spec())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := setupGoose(fsys, p.Spec()); err != nil {
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
func (p *postgresDialect) Down(ctx context.Context, dsn string, fsys fs.FS, dir string, target string) error {
	db, err := openPgxDB(dsn, p.Spec())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := setupGoose(fsys, p.Spec()); err != nil {
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
func (p *postgresDialect) Status(ctx context.Context, dsn string, fsys fs.FS, dir string, out io.Writer) error {
	db, err := openPgxDB(dsn, p.Spec())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := setupGoose(fsys, p.Spec()); err != nil {
		return err
	}
	_ = out // goose v3 пишет в свой logger; redirect — через goose.SetLogger
	return goose.StatusContext(ctx, db, dir)
}

// Create создаёт новый пустой SQL-файл миграции на диске (без подключения к БД).
func (p *postgresDialect) Create(physDir, name string) error {
	if name == "" {
		return errors.New("migration name is empty")
	}
	if physDir == "" {
		return errors.New("physical migrations directory is empty (--dir)")
	}
	// goose.Create не требует подключения к БД и BaseFS — пишет на диск.
	// SetDialect нужен только чтобы goose знал, какой шаблон комментариев писать.
	if err := goose.SetDialect(p.Spec().GooseDialect); err != nil {
		return fmt.Errorf("goose set dialect %q: %w", p.Spec().GooseDialect, err)
	}
	return goose.Create(nil, physDir, name, "sql")
}

// openPgxDB и setupGoose — общие helpers для postgres + cockroach (оба идут
// через pgx driver и goose-dialect="postgres"). Вынесены сюда (postgres.go),
// потому что postgres — primary impl; cockroach их переиспользует.

// openPgxDB открывает *sql.DB через pgx-driver по dsn (общий helper postgres+cockroach).
func openPgxDB(dsn string, spec DialectSpec) (*sql.DB, error) {
	db, err := sql.Open(spec.SQLDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db (driver=%s): %w", spec.SQLDriver, err)
	}
	return db, nil
}

// setupGoose настраивает goose: base-FS с миграциями + диалект (общий helper).
func setupGoose(fsys fs.FS, spec DialectSpec) error {
	goose.SetBaseFS(fsys)
	if err := goose.SetDialect(spec.GooseDialect); err != nil {
		return fmt.Errorf("goose set dialect %q: %w", spec.GooseDialect, err)
	}
	return nil
}
