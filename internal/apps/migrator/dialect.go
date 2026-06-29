// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package migrator — бизнес-логика отдельного бинаря cmd/migrator.
//
// dialect.go определяет ключевую абстракцию пакета — интерфейс [Dialect].
// Каждая поддерживаемая БД — отдельная реализация (`postgres.go`,
// `cockroach.go`); фабрика [NewDialect] выбирает реализацию по имени из
// CLI/конфига. Это позволяет per-dialect tweaks без if-ветвей внутри общего
// Runner'а.
//
// CLI-метадата диалекта (имя, goose-имя, driver-имя) вынесена в [DialectSpec] —
// внутренний descriptor, отдельный от runtime-поведения.
package migrator

import (
	"context"
	"fmt"
	"io"
	"io/fs"
)

// Dialect — абстракция SQL-диалекта для миграций.
//
// Реализации:
//   - [postgresDialect] (`postgres.go`) — основная, через goose + pgx driver;
//   - [cockroachDialect] (`cockroach.go`) — CockroachDB SQL-совместим с Postgres
//     wire protocol, но не поддерживает часть PG-фич (`EXCLUDE USING gist`,
//     `xmin`-OCC, `LISTEN/NOTIFY`), поэтому часть миграций kacho-vpc на нем пока
//     не проходит — см. шапку cockroach.go.
//
// Все методы принимают context.Context, DSN и embed.FS — это позволяет тестам
// подменять FS на `fstest.MapFS`, а боевому коду использовать
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

	// Status печатает примененные/непримененные миграции в логгер goose.
	// out зарезервирован под будущий redirect (goose v3 пишет в свой logger).
	Status(ctx context.Context, dsn string, fsys fs.FS, dir string, out io.Writer) error

	// Create создает пустой .sql-файл миграции на физическом диске (embed.FS
	// read-only). physDir — directory относительно cwd; name — суффикс имени.
	Create(physDir, name string) error

	// Spec возвращает CLI-метадату диалекта (имя, goose-имя, driver-имя для
	// sql.Open). Используется CLI для help / validation; runtime-логика
	// инкапсулирована в самих методах Up/Down/Status/Create.
	Spec() DialectSpec
}

// DialectSpec — описательная метадата диалекта для CLI-резолва и тестов.
//
// Это НЕ runtime-behaviour: реальная Up/Down/Status/Create логика живет в
// реализации [Dialect]-интерфейса. Spec нужен, чтобы:
//   - CLI мог напечатать список зарегистрированных диалектов в help;
//   - тесты могли проверить, что `--dialect cockroach` правильно резолвится;
//   - registry хранил пары name→constructor.
type DialectSpec struct {
	// Name — имя диалекта для CLI (postgres, cockroach, ...).
	Name string
	// GooseDialect — строка, ожидаемая goose.SetDialect. У cockroach значение
	// тоже "postgres" (он SQL-совместим с PG wire); хранится отдельно,
	// чтобы name в CLI мог быть "cockroach", а goose все еще получал "postgres".
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

// registry — name → factory. Заполняется init()'ом.
var registry = map[string]dialectFactory{
	SpecPostgres.Name:  func() Dialect { return newPostgresDialect() },
	SpecCockroach.Name: func() Dialect { return newCockroachDialect() },
}

// NewDialect — фабрика, возвращает реализацию [Dialect] по имени.
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

// ResolveDialect — обертка над [NewDialect]; возвращает [Dialect], чью метадату
// читают через Spec(). Сохранена как стабильная точка входа для cmd/migrator.
func ResolveDialect(name string) (Dialect, error) {
	return NewDialect(name)
}

func listDialects() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
