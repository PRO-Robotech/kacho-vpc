package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// TestIntegration_PrivateEndpoint_FK_RESTRICT (KAC-89): миграция 0024 добавила
// FK private_endpoints.{network_id, subnet_id, address_id} → networks/subnets/
// addresses(id) с ON DELETE RESTRICT. Раньше эти ссылки были обычным TEXT без
// FK (только software-validated в service-слое перед Operation) — Network /
// Subnet / Address можно было удалить из-под PE, оставив orphan-rows.
//
// Проверяем:
//   - DELETE parent (Network/Subnet/Address) при существующем PE → SQLSTATE 23503;
//   - INSERT PE с несуществующим network_id / subnet_id / address_id → 23503.
//
// G2 из vpc audit KAC-84.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer (raw DELETE через pool
// остаётся — это тест низкоуровневых FK constraints, мы намеренно идём в
// обход repo-слоя чтобы поймать SQLSTATE напрямую).
func TestIntegration_PrivateEndpoint_FK_RESTRICT(t *testing.T) {
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

	// Helper: outbox-emit опускаем — нам не важна (FK-тест, не watcher-тест).
	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	// --- setup: Network + Subnet + Address + PE ---
	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), ProjectID: "folder-1", Name: domain.RcNameVPC("net-pe-fk"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "folder-1", Name: domain.RcNameVPC("sub-pe-fk"),
		NetworkID: net.ID, ZoneID: "ru-central1-a", V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))

	addr := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), ProjectID: "folder-1", Name: domain.RcNameVPC("addr-pe-fk"),
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{Address: "203.0.113.42", ZoneID: "ru-central1-a"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))

	pe := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		ProjectID:   "folder-1",
		Name:        domain.RcNameVPC("pe-fk-1"),
		NetworkID:   net.ID,
		SubnetID:    sub.ID,
		AddressID:   addr.ID,
		IPAddress:   "10.0.0.5",
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.PrivateEndpoints().Insert(ctx, pe)
		return e
	}))

	// --- 1. DELETE address — FK 23503 на PE.address_id ---
	_, err = pool.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, addr.ID)
	require.Error(t, err, "DELETE address с PE должно блокироваться FK")
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code, "expected foreign_key_violation")
	assert.Equal(t, "private_endpoints_address_id_fkey", pgErr.ConstraintName)

	// --- 2. DELETE subnet — FK 23503 на PE.subnet_id ---
	_, err = pool.Exec(ctx, `DELETE FROM subnets WHERE id = $1`, sub.ID)
	require.Error(t, err, "DELETE subnet с PE должно блокироваться FK")
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code)
	assert.Equal(t, "private_endpoints_subnet_id_fkey", pgErr.ConstraintName)

	// --- 3. DELETE network — FK 23503 ---
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, net.ID)
	require.Error(t, err, "DELETE network с потомками должно блокироваться FK")
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code)
	assert.Contains(t,
		[]string{"private_endpoints_network_id_fkey", "subnets_network_id_fkey"},
		pgErr.ConstraintName,
		"expected FK to be one of the network referrers")

	// --- 4. INSERT PE с несуществующим network_id → FK violation (via writer) ---
	bad := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		ProjectID:   "folder-1",
		Name:        domain.RcNameVPC("pe-fk-bad-net"),
		NetworkID:   ids.NewID(ids.PrefixNetwork),
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	err = withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.PrivateEndpoints().Insert(ctx, bad)
		return e
	})
	require.Error(t, err, "INSERT PE с несуществующим network_id должен упасть на FK")
	assert.True(t,
		errors.Is(err, helpers.ErrFailedPrecondition) || errors.Is(err, helpers.ErrInternal) || errors.Is(err, helpers.ErrAlreadyExists) || isPgFKErr(err),
		"expected FK-related error, got %v", err)

	// --- 5. INSERT PE с несуществующим subnet_id → FK violation ---
	bad2 := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		ProjectID:   "folder-1",
		Name:        domain.RcNameVPC("pe-fk-bad-sub"),
		NetworkID:   net.ID,
		SubnetID:    ids.NewID(ids.PrefixSubnet),
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	err = withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.PrivateEndpoints().Insert(ctx, bad2)
		return e
	})
	require.Error(t, err, "INSERT PE с несуществующим subnet_id должен упасть на FK")
	assert.True(t,
		errors.Is(err, helpers.ErrFailedPrecondition) || isPgFKErr(err),
		"expected FK-related error, got %v", err)

	// --- 6. INSERT PE с несуществующим address_id → FK violation ---
	bad3 := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		ProjectID:   "folder-1",
		Name:        domain.RcNameVPC("pe-fk-bad-addr"),
		NetworkID:   net.ID,
		AddressID:   ids.NewID(ids.PrefixAddress),
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	err = withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.PrivateEndpoints().Insert(ctx, bad3)
		return e
	})
	require.Error(t, err, "INSERT PE с несуществующим address_id должен упасть на FK")
	assert.True(t,
		errors.Is(err, helpers.ErrFailedPrecondition) || isPgFKErr(err),
		"expected FK-related error, got %v", err)

	// --- 7. Корректное удаление снизу вверх ---
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.PrivateEndpoints().Delete(ctx, pe.ID)
	}))
	_, err = pool.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, addr.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM subnets WHERE id = $1`, sub.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, net.ID)
	require.NoError(t, err)

	// --- 7b. Изолированно проверяем private_endpoints_network_id_fkey ---
	netIso := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), ProjectID: "folder-1", Name: domain.RcNameVPC("net-iso-pe")}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, netIso)
		return e
	}))
	peIso := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		ProjectID:   "folder-1",
		Name:        domain.RcNameVPC("pe-iso"),
		NetworkID:   netIso.ID,
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.PrivateEndpoints().Insert(ctx, peIso)
		return e
	}))

	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, netIso.ID)
	require.Error(t, err)
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code)
	assert.Equal(t, "private_endpoints_network_id_fkey", pgErr.ConstraintName,
		"PE без других зависимостей должен бомбить именно PE→network FK")

	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.PrivateEndpoints().Delete(ctx, peIso.ID)
	}))
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, netIso.ID)
	require.NoError(t, err)

	// --- 8. Optional refs могут быть пустыми → NULL → FK не проверяется ---
	net2 := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), ProjectID: "folder-1", Name: domain.RcNameVPC("net-pe-fk-2")}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net2)
		return e
	}))

	peNoOpt := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		ProjectID:   "folder-1",
		Name:        domain.RcNameVPC("pe-fk-no-opt"),
		NetworkID:   net2.ID,
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.PrivateEndpoints().Insert(ctx, peNoOpt)
		return e
	}), "PE с пустыми subnet_id/address_id должен Insert'иться (NULLIF → NULL → FK пропускает)")

	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.PrivateEndpoints().Delete(ctx, peNoOpt.ID)
	}))
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, net2.ID)
	require.NoError(t, err)
}

// isPgFKErr — true если err оборачивает pgconn.PgError с кодом 23503.
func isPgFKErr(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}
