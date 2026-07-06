// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Тесты CLI миграций: покрывают только парсинг cobra-флагов и резолвинг
// диалекта/DSN. Реальный apply миграций — в integration-suite
// `internal/repo/...integration_test.go` (testcontainers Postgres + goose.Up).
// Здесь БД не открывается: тесты быстрые и не зависят от docker.
package main

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func emptyFS() fs.FS { return fstest.MapFS{} }

// runCommand — helper: парсит args, ловит ошибки cobra. Stdout/stderr
// захватывается для последующих assert'ов в тестах конкретных subcommand'ов.
func runCommand(t *testing.T, args []string, env map[string]string) (stdout, stderr string, err error) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	cmd := newRootCmd(emptyFS())
	var sout, serr bytes.Buffer
	cmd.SetOut(&sout)
	cmd.SetErr(&serr)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return sout.String(), serr.String(), err
}

func TestRootCmd_HelpDoesNotError(t *testing.T) {
	// `--help` отрабатывает чисто и печатает Use-строку.
	stdout, _, err := runCommand(t, []string{"--help"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "kacho-migrator") {
		t.Fatalf("expected help output to mention kacho-migrator, got: %q", stdout)
	}
	for _, sub := range []string{"up", "down", "status", "create"} {
		if !strings.Contains(stdout, sub) {
			t.Errorf("help output missing subcommand %q: %q", sub, stdout)
		}
	}
}

func TestUpCmd_ParsesTargetFlag(t *testing.T) {
	// Парсинг `up --target 10` не должен падать на flag-уровне; ошибка
	// допустима только из-за пустого DSN (наш namespace при unset env).
	// Проверка проще: запустить с явно невалидным dialect — cobra-парсер
	// корректно дойдет до RunE, а там NewDialect отдаст ошибку.
	_, _, err := runCommand(t, []string{
		"--dialect", "bogus-dialect",
		"--dsn", "postgres://x:y@z:1/d?sslmode=disable",
		"up", "--target", "10",
	}, nil)
	if err == nil {
		t.Fatal("expected error for unknown dialect, got nil")
	}
	if !strings.Contains(err.Error(), "unknown dialect") {
		t.Fatalf("expected 'unknown dialect' error, got: %v", err)
	}
}

func TestDownCmd_ParsesTargetFlag(t *testing.T) {
	_, _, err := runCommand(t, []string{
		"--dialect", "bogus",
		"--dsn", "postgres://x:y@z:1/d?sslmode=disable",
		"down", "--target", "5",
	}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown dialect") {
		t.Fatalf("got unexpected error: %v", err)
	}
}

func TestCreateCmd_RequiresNameArg(t *testing.T) {
	// `create` без позиционного аргумента → cobra-валидация падает на Args.
	_, _, err := runCommand(t, []string{
		"--dsn", "postgres://x:y@z:1/d?sslmode=disable",
		"create",
	}, nil)
	if err == nil {
		t.Fatal("expected error for missing name arg, got nil")
	}
	if !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Fatalf("expected cobra Args error, got: %v", err)
	}
}

func TestBuildRunner_DSNFromFlag(t *testing.T) {
	// Прямой unit-тест на buildRunner: --dsn явно задан → используется без
	// обращения к ENV / envconfig.
	opts := &rootOptions{dialect: "postgres", dsn: "postgres://u:p@h:5432/db?sslmode=disable"}
	r, err := buildRunner(opts, emptyFS())
	if err != nil {
		t.Fatalf("buildRunner failed: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestBuildRunner_DSNFromEnv(t *testing.T) {
	// --dsn пуст, ENV KACHO_MIGRATOR_DSN — выставлен → берется из ENV.
	t.Setenv(envDSN, "postgres://envuser:envpw@envhost:5432/envdb?sslmode=disable")
	opts := &rootOptions{dialect: "postgres"}
	r, err := buildRunner(opts, emptyFS())
	if err != nil {
		t.Fatalf("buildRunner failed: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestBuildRunner_UnknownDialect(t *testing.T) {
	opts := &rootOptions{dialect: "nosuch", dsn: "postgres://x"}
	_, err := buildRunner(opts, emptyFS())
	if err == nil || !strings.Contains(err.Error(), "unknown dialect") {
		t.Fatalf("expected unknown dialect error, got: %v", err)
	}
}

func TestBuildRunner_DSNFallbackToConfig(t *testing.T) {
	// --dsn пуст, ENV KACHO_MIGRATOR_DSN пуст, но KACHO_VPC_DB_PASSWORD
	// задан → buildRunner должен дойти до config.Load и собрать DSN из
	// envconfig (cfg.MigrateDSN()). Никаких ошибок не ожидается.
	t.Setenv("KACHO_VPC_DB_PASSWORD", "fallback-password")
	t.Setenv("KACHO_VPC_DB_HOST", "fallback-host")
	// envDSN явно НЕ выставляем — пусть будет тот, что в shell (обычно пуст).
	// Если шелл выставит KACHO_MIGRATOR_DSN — этот тест становится no-op
	// (берется ENV-DSN), что не ломает контракт fallback.
	opts := &rootOptions{dialect: "postgres"}
	r, err := buildRunner(opts, emptyFS())
	if err != nil {
		t.Fatalf("expected fallback to config to succeed, got: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}
