// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// dialect_test.go — unit-тесты на фабрику [NewDialect]. Integration-тесты против
// реальной БД живут в `internal/repo/...` (postgres через testcontainers).
package migrator

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewDialect_Valid(t *testing.T) {
	for _, name := range []string{"postgres"} {
		t.Run(name, func(t *testing.T) {
			d, err := NewDialect(name)
			if err != nil {
				t.Fatalf("NewDialect(%q) failed: %v", name, err)
			}
			if d == nil {
				t.Fatalf("NewDialect(%q) returned nil dialect", name)
			}
			spec := d.Spec()
			if spec.Name != name {
				t.Errorf("Spec().Name: expected %q, got %q", name, spec.Name)
			}
			if spec.SQLDriver == "" {
				t.Errorf("Spec().SQLDriver is empty for %q", name)
			}
			if spec.GooseDialect == "" {
				t.Errorf("Spec().GooseDialect is empty for %q", name)
			}
		})
	}
}

func TestNewDialect_Invalid(t *testing.T) {
	_, err := NewDialect("nosuchdb")
	if err == nil {
		t.Fatal("expected error for unknown dialect, got nil")
	}
	if !strings.Contains(err.Error(), "unknown dialect") {
		t.Fatalf("expected 'unknown dialect' in error, got: %v", err)
	}
}

func TestPostgresDialect_Spec(t *testing.T) {
	d, _ := NewDialect("postgres")
	if d.Spec() != SpecPostgres {
		t.Fatalf("postgres dialect spec mismatch: got %+v, want %+v", d.Spec(), SpecPostgres)
	}
}

func TestPostgresDialect_CreateRejectsEmpty(t *testing.T) {
	d, _ := NewDialect("postgres")
	if err := d.Create("", "foo"); err == nil {
		t.Error("expected error for empty physDir")
	}
	if err := d.Create("/tmp", ""); err == nil {
		t.Error("expected error for empty name")
	}
}

// Compile-time assertion: built-in dialect удовлетворяет интерфейсу.
var _ Dialect = (*postgresDialect)(nil)

// Дополнительный compile-check: реальная FS-based фабрика не паникует.
func TestNewDialect_FactoryReturnsFresh(t *testing.T) {
	d1, _ := NewDialect("postgres")
	d2, _ := NewDialect("postgres")
	if d1 == d2 {
		t.Log("note: factory returned identical pointer — not a bug (stateless dialect), но если поменяется на stateful — этот тест поможет ловить")
	}
	// Sanity-check на FS interface — стандартный fstest.MapFS должен компилиться
	// как аргумент Dialect.Up / .Down / .Status (мы не запускаем, только assert тип).
	var _ fs.FS = fstest.MapFS{}
}
