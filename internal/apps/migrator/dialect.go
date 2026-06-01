// Package migrator — бизнес-логика отдельного бинаря cmd/migrator (skill
// evgeniy §9 K.1–K.3, AP-9).
//
// dialect.go определяет ключевую абстракцию пакета — интерфейс [Dialect]
// (правило K.3: "migrator multi-dialect-ready"). Каждая поддерживаемая БД —
// отдельная реализация (`postgres.go`, `cockroach.go`); фабрика [NewDialect]
// выбирает реализацию по имени из CLI/конфига. Это позволяет per-dialect
// tweaks (например, рукописный xmin-OCC для cockroach в будущем) без if-ветвей
// внутри общего Runner'а.
//
// Контракт обратной совместимости: до KAC-94 в пакете жил `Dialect`-struct
// с полями {Name, GooseDialect, SQLDriver} как CLI-метадата. Эта метадата
// сохранена как [DialectSpec] (внутренний descriptor); внешний API
// (`migrator.New(Config{...})`, `migrator.ResolveDialect(name)`,
// `migrator.RegisterDialect(...)`) — стабильный, тот же.
package migrator

import (
	"context"
	"fmt"
	"io"
	"io/fs"
)

// Dialect — абстракция SQL-диалекта для миграций (skill evgeniy §9 K.3).
//
// Реализации:
//   - [postgresDialect] (`postgres.go`) — production, через goose + pgx driver;
//   - [cockroachDialect] (`cockroach.go`) — scaffold; CockroachDB SQL-совместим
//     с Postgres wire protocol, но НЕ поддерживает часть PG-фич
//     (`EXCLUDE USING gist`, `xmin`-OCC, `LISTEN/NOTIFY` semantics) —
//     production-ready требует переписать ряд миграций kacho-vpc под cockroach.
//
// Все методы принимают context.Context, DSN и embed.FS — это позволяет
// тестам подменять FS на `fstest.MapFS`, а production использовать
// `internal/migrations.FS`.
//
// Конструктор Dialect — [NewDialect].
type Dialect interface {
	// Up применяет миграции вверх. target=="" → до самой последней; иначе
	// до версии target (включительно).
	Up(ctx context.Context, dsn string, fsys fs.FS, dir string, target string) error

	// Down откатывает миграцию(и). target=="" → одна последняя; иначе до
	// версии target (включительно).
	Down(ctx context.Context, dsn string, fsys fs.FS, dir string, target string) error

	// Status печатает применённые/неприменённые миграции в логгер goose.
	// out зарезервирован под будущий redirect (goose v3 пишет в свой logger).
	Status(ctx context.Context, dsn string, fsys fs.FS, dir string, out io.Writer) error

	// Create создаёт пустой .sql-файл миграции на физическом диске (embed.FS
	// read-only). physDir — directory относительно cwd; name — суффикс имени.
	Create(physDir, name string) error

	// Spec возвращает CLI-метадату диалекта (имя, goose-имя, driver-имя для
	// sql.Open). Используется CLI для help / validation; runtime-логика
	// инкапсулирована в самих методах Up/Down/Status/Create.
	Spec() DialectSpec
}

// DialectSpec — описательная метадата диалекта для CLI-резолва и тестов
// (skill evgeniy §9 K.3, обратная совместимость с pre-KAC-94 API).
//
// Это НЕ runtime-behaviour: реальная Up/Down/Status/Create логика живёт
// в реализации [Dialect]-интерфейса. Spec нужен, чтобы:
//   - CLI мог напечатать список зарегистрированных диалектов в help;
//   - тесты могли проверить, что `--dialect cockroach` правильно резолвится;
//   - registry (см. [RegisterDialect]) хранил пары name→constructor.
type DialectSpec struct {
	// Name — имя диалекта для CLI (postgres, cockroach, ...).
	Name string
	// GooseDialect — строка, ожидаемая goose.SetDialect. У cockroach значение
	// тоже "postgres" (он SQL-совместим с PG wire); хранится отдельно,
	// чтобы name в CLI мог быть "cockroach", а goose всё ещё получал "postgres".
	GooseDialect string
	// SQLDriver — имя драйвера для sql.Open. Регистрируется через blank
	// import в main.go отдельного бинаря (`_ "github.com/jackc/pgx/v5/stdlib"`
	// регистрирует "pgx" driver и для postgres, и для cockroach).
	SQLDriver string
}

// Built-in spec'и — exposed для тестов и diagnostics.
var (
	SpecPostgres = DialectSpec{
		Name:         "postgres",
		GooseDialect: "postgres",
		SQLDriver:    "pgx",
	}
	SpecCockroach = DialectSpec{
		Name:         "cockroach",
		GooseDialect: "postgres", // CockroachDB SQL-совместим с PG wire
		SQLDriver:    "pgx",
	}
)

// dialectFactory — конструктор реализации [Dialect] по имени.
type dialectFactory func() Dialect

// registry — name → factory. Заполняется init()'ом и [RegisterDialect].
var registry = map[string]dialectFactory{
	SpecPostgres.Name:  func() Dialect { return newPostgresDialect() },
	SpecCockroach.Name: func() Dialect { return newCockroachDialect() },
}

// NewDialect — фабрика, возвращает реализацию [Dialect] по имени
// (skill evgeniy §9 K.3 буквально: «фабрика; supported postgres, cockroach»).
//
// Поддерживаемые: "postgres", "cockroach". Неизвестное имя → ошибка
// со списком зарегистрированных.
func NewDialect(name string) (Dialect, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown dialect %q (supported: %v)", name, listDialects())
	}
	return factory(), nil
}

// ResolveDialect — backwards-compat обёртка над [NewDialect]. До KAC-94 этот
// метод возвращал struct `Dialect{Name, GooseDialect, SQLDriver}`; теперь
// возвращает interface — но Spec() даёт ту же метадату. Существующий
// cmd/migrator/main.go использует только tag.Spec().Name (косвенно через
// Config.Dialect), так что breaking-change ограничен пакетом migrator.
func ResolveDialect(name string) (Dialect, error) {
	return NewDialect(name)
}

// RegisterDialect добавляет новый диалект в registry (для тестов / расширений).
// Принимает spec + factory; spec.Name — ключ.
//
// Перегрузка существующего ключа допустима (для тестов с заменой реализации
// на fake). Если spec.Name пуст — panic (это пакетная мисконфигурация, не
// runtime-условие).
func RegisterDialect(spec DialectSpec, factory func() Dialect) {
	if spec.Name == "" {
		panic("migrator.RegisterDialect: spec.Name is empty")
	}
	registry[spec.Name] = dialectFactory(factory)
}

// listDialects возвращает имена всех зарегистрированных диалектов.
func listDialects() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
