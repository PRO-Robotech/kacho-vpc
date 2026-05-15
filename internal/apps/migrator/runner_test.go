// internal/apps/migrator/runner_test.go — KAC-96.
//
// Unit-тесты на чистую логику Runner / Dialect / Config — без обращения к БД.
// Реальный apply покрыт integration-suite'ом в `internal/repo/...`.
package migrator

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestResolveDialect_Builtin(t *testing.T) {
	for _, name := range []string{"postgres", "cockroach"} {
		d, err := ResolveDialect(name)
		if err != nil {
			t.Errorf("ResolveDialect(%q) failed: %v", name, err)
			continue
		}
		if d.Name != name {
			t.Errorf("expected Name=%q, got %q", name, d.Name)
		}
		if d.SQLDriver == "" {
			t.Errorf("dialect %q has empty SQLDriver", name)
		}
	}
}

func TestResolveDialect_Unknown(t *testing.T) {
	_, err := ResolveDialect("nosuchdb")
	if err == nil || !strings.Contains(err.Error(), "unknown dialect") {
		t.Fatalf("expected 'unknown dialect' error, got %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	fs := fstest.MapFS{}
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "missing dialect", cfg: Config{DSN: "x", FS: fs, MigrationsDir: "."}, wantErr: "dialect"},
		{name: "missing dsn", cfg: Config{Dialect: DialectPostgres, FS: fs, MigrationsDir: "."}, wantErr: "dsn"},
		{name: "missing fs", cfg: Config{Dialect: DialectPostgres, DSN: "x", MigrationsDir: "."}, wantErr: "migrations FS"},
		{name: "missing dir", cfg: Config{Dialect: DialectPostgres, DSN: "x", FS: fs}, wantErr: "migrations dir"},
		{name: "ok", cfg: Config{Dialect: DialectPostgres, DSN: "x", FS: fs, MigrationsDir: "."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error to contain %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error for empty config, got nil")
	}
}

func TestParseTargetVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "10", want: 10},
		{in: "0010", want: 10}, // leading zeros — file-naming convention goose
		{in: "12345", want: 12345},
		{in: "abc", wantErr: true},
		{in: "-5", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseTargetVersion(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %d", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestRunner_CreateRejectsEmptyNameOrDir(t *testing.T) {
	r, err := New(Config{
		Dialect:       DialectPostgres,
		DSN:           "x",
		FS:            fstest.MapFS{},
		MigrationsDir: ".",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := r.Create("", "foo"); err == nil {
		t.Fatal("expected error for empty dir")
	}
	if err := r.Create("/tmp", ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestRegisterDialect(t *testing.T) {
	custom := Dialect{Name: "test-custom", GooseDialect: "postgres", SQLDriver: "pgx"}
	RegisterDialect(custom)
	d, err := ResolveDialect("test-custom")
	if err != nil {
		t.Fatalf("ResolveDialect failed: %v", err)
	}
	if d.GooseDialect != "postgres" {
		t.Fatalf("expected GooseDialect=postgres, got %q", d.GooseDialect)
	}
}
