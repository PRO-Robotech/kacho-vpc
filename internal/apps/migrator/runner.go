// Package migrator — бизнес-логика отдельного бинаря cmd/migrator (skill
// evgeniy §9 K.1–K.3, AP-9). До KAC-96 миграции запускались как subcommand
// основного бинаря `kacho-vpc migrate {up|down|status}` — это нарушение
// AP-9: одна точка сборки на use-case. Здесь живёт обёртка над goose,
// которую дёргает cmd/migrator/main.go (cobra).
//
// Поддерживает разные диалекты через интерфейс Dialect (K.3): сейчас одна
// реализация — postgres. Для добавления cockroach/mysql достаточно зарегистрировать
// новый dialect в RegisterDialect — runner и CLI остаются те же.
//
// Embed FS (`internal/migrations/*.sql`) принимается параметром Config.FS,
// чтобы runner не тянул прямой импорт `internal/migrations` (зависимость
// одно-направленная: cmd/migrator → internal/apps/migrator + internal/migrations,
// `internal/apps/migrator` ни к чему не привязан).
package migrator

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Dialect — абстракция SQL-диалекта для goose (правило K.3).
//
// goose.SetDialect принимает строку — это уже строковая абстракция, но мы
// заворачиваем её в типобезопасный enum + driver-имя для sql.Open, чтобы
// (a) защититься от typo на CLI, (b) обработать differences в driver-имени
// между БД (postgres → "pgx", cockroach → "pgx", mysql → "mysql", ...).
type Dialect struct {
	// Name — имя диалекта для CLI и goose (postgres, cockroach, mysql, ...).
	Name string
	// GooseDialect — строка для goose.SetDialect. У cockroach значение тоже
	// "postgres" (он SQL-совместим); хранится отдельно, чтобы name в CLI
	// мог быть «cockroach», а goose всё ещё получал «postgres».
	GooseDialect string
	// SQLDriver — имя драйвера для sql.Open. Регистрируется через blank
	// import в main.go отдельного бинаря (мы импортируем "pgx/v5/stdlib"
	// → "pgx" driver доступен для postgres и cockroach).
	SQLDriver string
}

// Built-in диалекты. RegisterDialect позволяет добавить новые runtime'но.
var (
	DialectPostgres = Dialect{
		Name:         "postgres",
		GooseDialect: "postgres",
		SQLDriver:    "pgx",
	}
	// DialectCockroach — заготовка под K.3 (поддержка нескольких БД). Сейчас
	// никем не используется и не покрыта integration-тестами; помечена
	// experimental. При первом реальном использовании — добавить cockroach
	// integration test и убрать комментарий «experimental».
	DialectCockroach = Dialect{
		Name:         "cockroach",
		GooseDialect: "postgres", // CockroachDB SQL-совместим с PG wire
		SQLDriver:    "pgx",
	}
)

var dialects = map[string]Dialect{
	DialectPostgres.Name:  DialectPostgres,
	DialectCockroach.Name: DialectCockroach,
}

// RegisterDialect добавляет новый диалект (для тестов / расширений; main.go
// сейчас не вызывает — у нас только postgres).
func RegisterDialect(d Dialect) {
	dialects[d.Name] = d
}

// ResolveDialect возвращает Dialect по имени (case-sensitive). Ошибка —
// если имя неизвестно; cobra на это маппит exit-code != 0.
func ResolveDialect(name string) (Dialect, error) {
	d, ok := dialects[name]
	if !ok {
		return Dialect{}, fmt.Errorf("unknown dialect %q (registered: %v)", name, listDialects())
	}
	return d, nil
}

func listDialects() []string {
	out := make([]string, 0, len(dialects))
	for k := range dialects {
		out = append(out, k)
	}
	return out
}

// Config — параметры одного запуска runner'а. Заполняется cmd/migrator/main.go
// из cobra-флагов / ENV / viper-config.
type Config struct {
	// Dialect — резолвленный диалект (через ResolveDialect).
	Dialect Dialect
	// DSN — строка подключения для sql.Open(Dialect.SQLDriver, DSN).
	// Тот же DSN, что использует kacho-vpc для pgxpool (без pool_max_conns
	// — sql.Open его не понимает, см. config.MigrateDSN). У cobra-front'а
	// поле читается из --dsn / ENV KACHO_MIGRATOR_DSN.
	DSN string
	// FS — embed.FS с .sql миграциями (передаётся cmd/migrator/main.go из
	// internal/migrations.FS). Так runner не зависит напрямую от vpc-specific
	// пакета и переиспользуется любой сервис, если будет нужно.
	FS fs.FS
	// MigrationsDir — путь внутри FS, где лежат .sql файлы. Для embed.FS
	// корня — ".".
	MigrationsDir string
}

// Validate проверяет минимально необходимые поля перед обращением к goose.
func (c Config) Validate() error {
	if c.Dialect.Name == "" {
		return errors.New("dialect is not set")
	}
	if c.DSN == "" {
		return errors.New("dsn is empty (set --dsn or KACHO_MIGRATOR_DSN)")
	}
	if c.FS == nil {
		return errors.New("migrations FS is nil")
	}
	if c.MigrationsDir == "" {
		return errors.New("migrations dir is empty")
	}
	return nil
}

// Runner — высокоуровневая обёртка над goose. Один экземпляр на жизнь
// процесса, методы безопасны для последовательных вызовов; concurrent
// использование не предполагается (cobra гоняет одну команду за раз).
type Runner struct {
	cfg Config
}

// New собирает Runner; cfg валидируется здесь же — позже goose-вызов
// не возвращает «friendly» ошибку, если, например, FS==nil.
func New(cfg Config) (*Runner, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Runner{cfg: cfg}, nil
}

// openDB и setupGoose — выносим в helper, чтобы каждая команда не дублировала
// boilerplate. Caller обязан вызвать db.Close() (defer).
func (r *Runner) openDB() (*sql.DB, error) {
	db, err := sql.Open(r.cfg.Dialect.SQLDriver, r.cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open db (driver=%s): %w", r.cfg.Dialect.SQLDriver, err)
	}
	return db, nil
}

func (r *Runner) setupGoose() error {
	goose.SetBaseFS(r.cfg.FS)
	if err := goose.SetDialect(r.cfg.Dialect.GooseDialect); err != nil {
		return fmt.Errorf("goose set dialect %q: %w", r.cfg.Dialect.GooseDialect, err)
	}
	return nil
}

// Up прогоняет все миграции вверх. target=="" → до самой последней.
// target!="" → до указанной версии (включительно); goose ожидает int64.
func (r *Runner) Up(target string) error {
	if err := r.setupGoose(); err != nil {
		return err
	}
	db, err := r.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if target == "" {
		return goose.Up(db, r.cfg.MigrationsDir)
	}
	version, perr := parseTargetVersion(target)
	if perr != nil {
		return perr
	}
	return goose.UpTo(db, r.cfg.MigrationsDir, version)
}

// Down откатывает миграцию(и). target=="" → одна последняя (goose.Down
// default); target!="" → до версии (включительно).
func (r *Runner) Down(target string) error {
	if err := r.setupGoose(); err != nil {
		return err
	}
	db, err := r.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if target == "" {
		return goose.Down(db, r.cfg.MigrationsDir)
	}
	version, perr := parseTargetVersion(target)
	if perr != nil {
		return perr
	}
	return goose.DownTo(db, r.cfg.MigrationsDir, version)
}

// Status печатает таблицу применённых/неприменённых миграций. goose сам
// форматирует вывод (через log.Println внутри); чтобы не дублировать,
// просто проксируем. out не используется goose (он пишет в свой logger);
// оставлено в сигнатуре под будущую кастомизацию (capture buffer в тестах
// делаем через goose.SetLogger).
func (r *Runner) Status(out io.Writer) error {
	if err := r.setupGoose(); err != nil {
		return err
	}
	_ = out // goose v3 пишет в свой logger, redirect — через goose.SetLogger
	db, err := r.openDB()
	if err != nil {
		return err
	}
	defer db.Close()
	return goose.Status(db, r.cfg.MigrationsDir)
}

// Create создаёт новый sql-файл миграции на диске (в указанной директории
// — это physical dir, не FS). embed.FS read-only, поэтому cobra передаёт
// сюда явный path параметром CLI (--dir). По умолчанию main.go ставит
// `internal/migrations`. Тип `sql` фиксирован (Go-миграции у нас не
// используются).
func (r *Runner) Create(physDir, name string) error {
	if name == "" {
		return errors.New("migration name is empty")
	}
	if physDir == "" {
		return errors.New("physical migrations directory is empty (--dir)")
	}
	if err := r.setupGoose(); err != nil {
		return err
	}
	return goose.Create(nil, physDir, name, "sql")
}

// parseTargetVersion — goose использует int64 для версии (timestamp или
// 4-digit prefix файла). Принимаем строку с CLI, чтобы пользователь мог
// написать «0010» как в имени файла; конвертация — fmt.Sscanf вместо
// strconv.ParseInt для устойчивости к leading zeros.
func parseTargetVersion(s string) (int64, error) {
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, fmt.Errorf("parse target version %q: %w", s, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("target version must be non-negative, got %d", v)
	}
	return v, nil
}
