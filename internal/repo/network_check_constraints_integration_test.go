// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Baseline 0001_initial.sql несет DB-уровневые CHECK на name regex и длину
// description. Эти тесты идут в обход domain.Network.Validate()
// (прямой INSERT через writer.Networks().Insert, который Validate не зовет) и
// убеждаются, что DB-CHECK срабатывает: 23514 → helpers.WrapPgErr →
// helpers.ErrInvalidArg.

func TestIntegration_NetworkRepo_CheckConstraints(t *testing.T) {
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

	// 1. Корректное имя проходит.
	good := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "project-check",
		Name:      domain.RcNameVPC("good-name"),
	}
	require.NoError(t, insertNet(t, good))

	// 2. Имя начинающееся с цифры — отклоняется DB-CHECK regex.
	bad := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "project-check",
		Name:      domain.RcNameVPC("1bad"),
	}
	err = insertNet(t, bad)
	require.Error(t, err, "name начинающееся с цифры должно быть отклонено CHECK")
	require.Truef(t, errors.Is(err, helpers.ErrInvalidArg),
		"expected helpers.ErrInvalidArg from CHECK violation, got: %v", err)

	// 3. Description длиннее 256 chars — отклоняется DB-CHECK length.
	longDesc := make([]byte, 257)
	for i := range longDesc {
		longDesc[i] = 'a'
	}
	tooLong := &domain.Network{
		ID:          ids.NewID(ids.PrefixNetwork),
		ProjectID:   "project-check",
		Name:        domain.RcNameVPC("long-desc"),
		Description: domain.RcDescription(longDesc),
	}
	err = insertNet(t, tooLong)
	require.Error(t, err, "description >256 chars должно быть отклонено CHECK")
	require.Truef(t, errors.Is(err, helpers.ErrInvalidArg),
		"expected helpers.ErrInvalidArg from CHECK violation, got: %v", err)

	// 4. Пустое имя — OK (разрешительная политика валидации допускает empty).
	empty := &domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "project-check",
		Name:      domain.RcNameVPC(""),
	}
	require.NoError(t, insertNet(t, empty), "empty name разрешен permissive name-regex (пустое имя допустимо)")
}
