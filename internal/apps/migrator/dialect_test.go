// internal/apps/migrator/dialect_test.go — KAC-94, skill evgeniy §9 K.3.
//
// Unit-тесты на фабрику [NewDialect] и [ResolveDialect] (backwards-compat
// alias). Integration-тесты против реальной БД — в `internal/repo/...`
// (postgres через testcontainers); cockroach integration — TBD под отдельный
// тикет, когда production-need будет.
package migrator

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewDialect_Valid(t *testing.T) {
	for _, name := range []string{"postgres", "cockroach"} {
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

func TestResolveDialect_Alias(t *testing.T) {
	// ResolveDialect — backwards-compat alias для NewDialect.
	d, err := ResolveDialect("postgres")
	if err != nil {
		t.Fatalf("ResolveDialect(postgres) failed: %v", err)
	}
	if d.Spec().Name != "postgres" {
		t.Fatalf("expected Name=postgres, got %q", d.Spec().Name)
	}

	_, err = ResolveDialect("unknown")
	if err == nil {
		t.Fatal("expected error for unknown dialect")
	}
}

func TestRegisterDialect(t *testing.T) {
	spec := DialectSpec{Name: "test-custom-" + t.Name(), GooseDialect: "postgres", SQLDriver: "pgx"}
	RegisterDialect(spec, func() Dialect { return &fakeDialect{spec: spec} })

	d, err := NewDialect(spec.Name)
	if err != nil {
		t.Fatalf("NewDialect(%q) failed: %v", spec.Name, err)
	}
	if d.Spec().Name != spec.Name {
		t.Fatalf("expected Name=%q, got %q", spec.Name, d.Spec().Name)
	}
	if d.Spec().GooseDialect != "postgres" {
		t.Fatalf("expected GooseDialect=postgres, got %q", d.Spec().GooseDialect)
	}
}

func TestRegisterDialect_PanicsOnEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty spec.Name")
		}
	}()
	RegisterDialect(DialectSpec{}, func() Dialect { return &fakeDialect{} })
}

func TestPostgresDialect_Spec(t *testing.T) {
	d, _ := NewDialect("postgres")
	if d.Spec() != SpecPostgres {
		t.Fatalf("postgres dialect spec mismatch: got %+v, want %+v", d.Spec(), SpecPostgres)
	}
}

func TestCockroachDialect_Spec(t *testing.T) {
	d, _ := NewDialect("cockroach")
	spec := d.Spec()
	if spec != SpecCockroach {
		t.Fatalf("cockroach dialect spec mismatch: got %+v, want %+v", spec, SpecCockroach)
	}
	// CockroachDB должен использовать goose-dialect "postgres" (wire-compat).
	if spec.GooseDialect != "postgres" {
		t.Errorf("cockroach must use GooseDialect=postgres (wire-compat), got %q", spec.GooseDialect)
	}
	// И тот же pgx driver.
	if spec.SQLDriver != "pgx" {
		t.Errorf("cockroach must use SQLDriver=pgx, got %q", spec.SQLDriver)
	}
}

func TestCockroachDialect_CreateRejectsEmpty(t *testing.T) {
	// Create — единственный метод, который не открывает БД; можно дёрнуть
	// без testcontainers и проверить validation-ветви.
	d, _ := NewDialect("cockroach")
	if err := d.Create("", "foo"); err == nil {
		t.Error("expected error for empty physDir")
	}
	if err := d.Create("/tmp", ""); err == nil {
		t.Error("expected error for empty name")
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

// fakeDialect — пустая реализация для теста [RegisterDialect].
type fakeDialect struct{ spec DialectSpec }

func (f *fakeDialect) Spec() DialectSpec                                            { return f.spec }
func (f *fakeDialect) Up(_ context.Context, _ string, _ fs.FS, _, _ string) error   { return nil }
func (f *fakeDialect) Down(_ context.Context, _ string, _ fs.FS, _, _ string) error { return nil }
func (f *fakeDialect) Status(_ context.Context, _ string, _ fs.FS, _ string, _ io.Writer) error {
	return nil
}
func (f *fakeDialect) Create(_, _ string) error { return nil }

// Compile-time assertion: оба built-in dialect'а удовлетворяют интерфейсу.
var (
	_ Dialect = (*postgresDialect)(nil)
	_ Dialect = (*cockroachDialect)(nil)
	_ Dialect = (*fakeDialect)(nil)
)

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
