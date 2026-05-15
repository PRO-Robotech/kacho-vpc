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
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
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
func TestIntegration_PrivateEndpoint_FK_RESTRICT(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	nr := repo.NewNetworkRepo(pool)
	sr := repo.NewSubnetRepo(pool)
	ar := repo.NewAddressRepo(pool)
	per := repo.NewPrivateEndpointRepo(pool)

	// --- setup: Network + Subnet + Address + PE ---
	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-1", Name: domain.RcNameVPC("net-pe-fk"),
	}
	_, err = nr.Insert(ctx, net)
	require.NoError(t, err)

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-1", Name: domain.RcNameVPC("sub-pe-fk"),
		NetworkID: net.ID, ZoneID: "ru-central1-a", V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, err = sr.Insert(ctx, sub)
	require.NoError(t, err)

	addr := &domain.Address{
		ID: ids.NewID(ids.PrefixAddress), FolderID: "folder-1", Name: domain.RcNameVPC("addr-pe-fk"),
		Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{Address: "203.0.113.42", ZoneID: "ru-central1-a"},
	}
	_, err = ar.Insert(ctx, addr)
	require.NoError(t, err)

	pe := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		FolderID:    "folder-1",
		Name:        domain.RcNameVPC("pe-fk-1"),
		NetworkID:   net.ID,
		SubnetID:    sub.ID,
		AddressID:   addr.ID,
		IPAddress:   "10.0.0.5",
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	_, err = per.Insert(ctx, pe)
	require.NoError(t, err)

	// --- 1. DELETE address — FK 23503 на PE.address_id ---
	// (Address не имеет других зависимостей в этом setup'е, кроме нашего PE,
	// поэтому FK PE.address_id срабатывает первым.)
	_, err = pool.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, addr.ID)
	require.Error(t, err, "DELETE address с PE должно блокироваться FK")
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code, "expected foreign_key_violation")
	assert.Equal(t, "private_endpoints_address_id_fkey", pgErr.ConstraintName)

	// --- 2. DELETE subnet — FK 23503 на PE.subnet_id ---
	// Subnet ссылается на Network, но другие потомки (NIC/Address-internal/...)
	// мы не создавали → единственный блокирующий FK — наш PE.subnet_id.
	_, err = pool.Exec(ctx, `DELETE FROM subnets WHERE id = $1`, sub.ID)
	require.Error(t, err, "DELETE subnet с PE должно блокироваться FK")
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code)
	assert.Equal(t, "private_endpoints_subnet_id_fkey", pgErr.ConstraintName)

	// --- 3. DELETE network — FK 23503 ---
	// У Network потомки = {Subnet (FK от subnets), PE (FK от private_endpoints)}.
	// Subnet'ный FK сработает первым по детерминированному физическому порядку
	// constraint'ов; чтобы изолировать PE-FK, сначала удалим зависимые Subnet+PE,
	// потом создадим Network без Subnet, привяжем к нему PE без subnet_id, и
	// проверим, что DELETE network блокируется именно PE-FK.
	// Альтернативно: предварительно DROP Subnet→Network FK — но это нарушит
	// схему. Идём первым путём.
	// Просто проверим, что при попытке DELETE Network получим 23503 с одним из
	// двух known constraint'ов (subnets_network_id_fkey OR private_endpoints_network_id_fkey),
	// — это всё ещё доказывает что FK работает; за изолированный PE-network-FK
	// отвечает отдельный шаг ниже.
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, net.ID)
	require.Error(t, err, "DELETE network с потомками должно блокироваться FK")
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code)
	assert.Contains(t,
		[]string{"private_endpoints_network_id_fkey", "subnets_network_id_fkey"},
		pgErr.ConstraintName,
		"expected FK to be one of the network referrers")

	// --- 4. INSERT PE с несуществующим network_id → 23503 ---
	bad := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		FolderID:    "folder-1",
		Name:        domain.RcNameVPC("pe-fk-bad-net"),
		NetworkID:   ids.NewID(ids.PrefixNetwork), // не существует
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	_, err = per.Insert(ctx, bad)
	require.Error(t, err, "INSERT PE с несуществующим network_id должен упасть на FK")
	// Repo маппит FK через wrapPgErr → ErrFailedPrecondition (или Repo
	// возвращает sentinel — здесь нам важно, что Insert НЕ прошёл).
	assert.True(t,
		errors.Is(err, ports.ErrFailedPrecondition) || errors.Is(err, ports.ErrInternal) || errors.Is(err, ports.ErrAlreadyExists) || isPgFKErr(err),
		"expected FK-related error, got %v", err)

	// --- 5. INSERT PE с несуществующим subnet_id → 23503 ---
	bad2 := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		FolderID:    "folder-1",
		Name:        domain.RcNameVPC("pe-fk-bad-sub"),
		NetworkID:   net.ID,
		SubnetID:    ids.NewID(ids.PrefixSubnet), // не существует
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	_, err = per.Insert(ctx, bad2)
	require.Error(t, err, "INSERT PE с несуществующим subnet_id должен упасть на FK")
	assert.True(t,
		errors.Is(err, ports.ErrFailedPrecondition) || isPgFKErr(err),
		"expected FK-related error, got %v", err)

	// --- 6. INSERT PE с несуществующим address_id → 23503 ---
	bad3 := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		FolderID:    "folder-1",
		Name:        domain.RcNameVPC("pe-fk-bad-addr"),
		NetworkID:   net.ID,
		AddressID:   ids.NewID(ids.PrefixAddress), // не существует
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	_, err = per.Insert(ctx, bad3)
	require.Error(t, err, "INSERT PE с несуществующим address_id должен упасть на FK")
	assert.True(t,
		errors.Is(err, ports.ErrFailedPrecondition) || isPgFKErr(err),
		"expected FK-related error, got %v", err)

	// --- 7. Корректное удаление снизу вверх — PE → Address → Subnet → Network ---
	require.NoError(t, per.Delete(ctx, pe.ID))
	// После удаления PE FK сняты — parent-rows можно удалить.
	_, err = pool.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, addr.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM subnets WHERE id = $1`, sub.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, net.ID)
	require.NoError(t, err)

	// --- 7b. Изолированно проверяем private_endpoints_network_id_fkey ---
	// Network без Subnet'ов с одним PE (без subnet_id/address_id) → DELETE
	// network упирается строго в PE→network FK.
	netIso := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-1", Name: domain.RcNameVPC("net-iso-pe")}
	_, err = nr.Insert(ctx, netIso)
	require.NoError(t, err)
	peIso := &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		FolderID:    "folder-1",
		Name:        domain.RcNameVPC("pe-iso"),
		NetworkID:   netIso.ID,
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	_, err = per.Insert(ctx, peIso)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, netIso.ID)
	require.Error(t, err)
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	assert.Equal(t, "23503", pgErr.Code)
	assert.Equal(t, "private_endpoints_network_id_fkey", pgErr.ConstraintName,
		"PE без других зависимостей должен бомбить именно PE→network FK")

	// Cleanup iso.
	require.NoError(t, per.Delete(ctx, peIso.ID))
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, netIso.ID)
	require.NoError(t, err)

	// --- 8. Optional refs (subnet_id/address_id) могут быть пустыми → NULL в БД, FK не проверяется ---
	net2 := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-1", Name: domain.RcNameVPC("net-pe-fk-2")}
	_, err = nr.Insert(ctx, net2)
	require.NoError(t, err)

	peNoOpt := &domain.PrivateEndpoint{
		ID:        ids.NewID(ids.PrefixPrivateEndpoint),
		FolderID:  "folder-1",
		Name:      domain.RcNameVPC("pe-fk-no-opt"),
		NetworkID: net2.ID,
		// SubnetID / AddressID — empty → должны пойти в БД как NULL (NULLIF в Insert),
		// FK с MATCH SIMPLE пропускает NULL.
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusPending,
	}
	_, err = per.Insert(ctx, peNoOpt)
	require.NoError(t, err, "PE с пустыми subnet_id/address_id должен Insert'иться (NULLIF → NULL → FK пропускает)")

	// Cleanup.
	require.NoError(t, per.Delete(ctx, peNoOpt.ID))
	_, err = pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, net2.ID)
	require.NoError(t, err)
}

// isPgFKErr — true если err оборачивает pgconn.PgError с кодом 23503.
// Repo маппит FK через wrapPgErr → ErrFailedPrecondition, но в тесте
// допускаем и raw pgErr (на случай если оборачивающая обёртка изменится).
func isPgFKErr(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}
