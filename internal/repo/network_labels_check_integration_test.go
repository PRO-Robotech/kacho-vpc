package repo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// KAC-94 (Wave 2 batch D, skill evgeniy §5 E.2): миграция
// 0033_labels_check_constraints добавляет DB-уровневый CHECK на JSONB
// `labels`-поле всех 8 VPC-таблиц через helper-функцию kacho_labels_valid().
// Эти тесты идут в обход domain.ValidateLabels (прямой INSERT через writer) и
// убеждаются что DB-CHECK ловит cardinality / key-regex / value-length
// нарушения — financial backstop для bug'ов в app-коде / внешних writers.
//
// SQLSTATE 23514 → helpers.WrapPgErr → helpers.ErrInvalidArg.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.

func TestIntegration_NetworkRepo_LabelsCheckConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	defer r.Close()

	insertNet := func(t *testing.T, n *domain.Network) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		_, err = w.Networks().Insert(ctx, n)
		if err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	// 1. Пустой labels (default '{}'::jsonb) — CHECK проходит.
	emptyNet := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "folder-labels",
		Name:      domain.RcNameVPC("empty-labels"),
		Labels:    domain.LabelsFromMap(nil),
	}
	require.NoError(t, insertNet(t, emptyNet), "empty labels должен проходить CHECK")

	// 2. Валидные labels — CHECK проходит. Используем ключи, демонстрирующие
	//    полный character class: lowercase letters, digits, '-', '_', '.', '/',
	//    '\', '@' (Go regex `^[a-z][-_./\\@a-z0-9]{0,62}$`).
	validNet := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "folder-labels",
		Name:      domain.RcNameVPC("valid-labels"),
		Labels: domain.LabelsFromMap(map[string]string{
			"env":              "prod",
			"team-name":        "platform",
			"path.to/key\\sub": "complex-key-still-valid",
			"empty-val":        "",
			"a@b":              "v",
		}),
	}
	require.NoError(t, insertNet(t, validNet), "валидные labels должны проходить CHECK")

	// 3. 65 пар — cardinality нарушение, CHECK отбивает.
	tooMany := make(map[string]string, 65)
	for i := 0; i < 65; i++ {
		k := "k" + padDigits(i, 4)
		tooMany[k] = "v"
	}
	tooManyNet := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "folder-labels",
		Name:      domain.RcNameVPC("too-many"),
		Labels:    domain.LabelsFromMap(tooMany),
	}
	err = insertNet(t, tooManyNet)
	require.Error(t, err, "labels с 65 парами должно отбиваться CHECK (cardinality > 64)")
	require.Truef(t, errors.Is(err, helpers.ErrInvalidArg),
		"expected ErrInvalidArg from CHECK violation (cardinality), got: %v", err)

	// 4. Ключ нарушает regex (uppercase в начале) — CHECK отбивает.
	badKeyNet := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "folder-labels",
		Name:      domain.RcNameVPC("bad-key"),
		Labels: domain.LabelsFromMap(map[string]string{
			"Bad-Key": "v",
		}),
	}
	err = insertNet(t, badKeyNet)
	require.Error(t, err, "labels с key uppercase-start должно отбиваться CHECK (regex)")
	require.Truef(t, errors.Is(err, helpers.ErrInvalidArg),
		"expected ErrInvalidArg from CHECK violation (key regex), got: %v", err)

	// 5. Значение длиной 64 — CHECK отбивает (max 63).
	longVal := strings.Repeat("a", 64)
	badValNet := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "folder-labels",
		Name:      domain.RcNameVPC("bad-val"),
		Labels: domain.LabelsFromMap(map[string]string{
			"k": longVal,
		}),
	}
	err = insertNet(t, badValNet)
	require.Error(t, err, "labels с value длиной 64 должно отбиваться CHECK (length > 63)")
	require.Truef(t, errors.Is(err, helpers.ErrInvalidArg),
		"expected ErrInvalidArg from CHECK violation (value length), got: %v", err)

	// 6. Edge: value длиной ровно 63 — OK (boundary).
	okBoundaryNet := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "folder-labels",
		Name:      domain.RcNameVPC("ok-boundary"),
		Labels: domain.LabelsFromMap(map[string]string{
			"k": strings.Repeat("a", 63),
		}),
	}
	require.NoError(t, insertNet(t, okBoundaryNet), "value длиной ровно 63 byte — boundary OK")
}

func padDigits(n, width int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+(n%10))) + s
		n /= 10
	}
	for len(s) < width {
		s = "0" + s
	}
	return s
}
