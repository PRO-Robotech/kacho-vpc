package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// KAC-99 / KAC-94 (Wave 2 pilot, skill evgeniy §5 E.2): миграция
// 0025_network_check_constraints добавляет DB-уровневые CHECK на name regex
// и description length. Эти тесты идут в обход domain.Network.Validate()
// (прямой INSERT через repo, поля которого помимо вызова `Validate` ничего не
// проверяют) — и убеждаются что DB-CHECK срабатывает.
//
// 23514 → wrapPgErr → repo.ErrInvalidArg.

func TestIntegration_NetworkRepo_CheckConstraints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	netRepo := repo.NewNetworkRepo(pool)

	// 1. Корректное имя проходит.
	good := &domain.Network{
		ID:       ids.NewID(ids.PrefixNetwork),
		FolderID: "folder-check",
		Name:     domain.RcNameVPC("good-name"),
	}
	_, err = netRepo.Insert(ctx, good)
	require.NoError(t, err)

	// 2. Имя начинающееся с цифры — отклоняется DB-CHECK regex. Domain Validate
	//    тоже отклонил бы, но repo.Insert его не зовёт; полагаемся на DB.
	bad := &domain.Network{
		ID:       ids.NewID(ids.PrefixNetwork),
		FolderID: "folder-check",
		Name:     domain.RcNameVPC("1bad"),
	}
	_, err = netRepo.Insert(ctx, bad)
	require.Error(t, err, "name начинающееся с цифры должно быть отклонено CHECK")
	require.Truef(t, errors.Is(err, repo.ErrInvalidArg),
		"expected repo.ErrInvalidArg from CHECK violation, got: %v", err)

	// 3. Description длиннее 256 chars — отклоняется DB-CHECK length.
	longDesc := make([]byte, 257)
	for i := range longDesc {
		longDesc[i] = 'a'
	}
	tooLong := &domain.Network{
		ID:          ids.NewID(ids.PrefixNetwork),
		FolderID:    "folder-check",
		Name:        domain.RcNameVPC("long-desc"),
		Description: domain.RcDescription(longDesc),
	}
	_, err = netRepo.Insert(ctx, tooLong)
	require.Error(t, err, "description >256 chars должно быть отклонено CHECK")
	require.Truef(t, errors.Is(err, repo.ErrInvalidArg),
		"expected repo.ErrInvalidArg from CHECK violation, got: %v", err)

	// 4. Empty name — OK (verbatim YC permissive allows empty).
	empty := &domain.Network{
		ID:       ids.NewID(ids.PrefixNetwork),
		FolderID: "folder-check",
		Name:     domain.RcNameVPC(""),
	}
	_, err = netRepo.Insert(ctx, empty)
	require.NoError(t, err, "empty name разрешён permissive YC regex")
}
