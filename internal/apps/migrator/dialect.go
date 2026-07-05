// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package migrator — бизнес-логика отдельного бинаря cmd/migrator.
//
// dialect.go определяет абстракцию пакета — интерфейс [Dialect]. Продукт
// Postgres-only (rules: Postgres 16, database-per-service), поэтому реализация
// одна — [postgresDialect] (`postgres.go`); фабрика [NewDialect] резолвит ее по
// имени из CLI/конфига. Интерфейс — тонкий seam вокруг goose-конфигурации
// (goose-имя / driver / Up-Down-Status-Create), а не задел под мульти-БД: второй
// диалект добавляется только когда станет реальным требованием (non-negotiable
// #11 — без speculative-абстракций).
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
// Реализация одна: [postgresDialect] (`postgres.go`) — через goose + pgx driver.
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
//   - тесты могли проверить, что `--dialect postgres` правильно резолвится;
//   - registry хранил пары name→constructor.
type DialectSpec struct {
	// Name — имя диалекта для CLI (postgres).
	Name string
	// GooseDialect — строка, ожидаемая goose.SetDialect.
	GooseDialect string
	// SQLDriver — имя драйвера для sql.Open. Регистрируется через blank
	// import в main.go отдельного бинаря (`_ "github.com/jackc/pgx/v5/stdlib"`
	// регистрирует "pgx" driver).
	SQLDriver string
}

// Built-in spec — exposed для тестов и diagnostics.
var SpecPostgres = DialectSpec{
	Name:         "postgres",
	GooseDialect: "postgres",
	SQLDriver:    "pgx",
}

// dialectFactory — конструктор реализации [Dialect] по имени.
type dialectFactory func() Dialect

// registry — name → factory.
var registry = map[string]dialectFactory{
	SpecPostgres.Name: func() Dialect { return newPostgresDialect() },
}

// NewDialect — фабрика, возвращает реализацию [Dialect] по имени.
//
// Поддерживаемый: "postgres". Неизвестное имя → ошибка со списком
// зарегистрированных.
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
