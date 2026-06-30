// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Два конкурентных AddCidrBlocks на одну подсеть не должны терять изменения друг
// друга. AddCidrBlocks — read-modify-write (Get merged-set → SetCidrBlocks).
// Без row-lock оба читают один snapshot и второй commit затирает CIDR первого
// (lost-update). GetForUpdate сериализует доступ.
//
// Choreography (детерминированно ловит lost-update):
//  1. TX-A берет row-lock через GetForUpdate (читает [.0]).
//  2. Горутина TX-B вызывает GetForUpdate(+merge "2"+set+commit) — под FOR UPDATE
//     блокируется на шаге Get до commit TX-A; на голом Get прочитала бы [.0]
//     сразу, а заблокировалась бы уже на SetCidrBlocks (lock от A) и затем
//     перезаписала бы добавленный A блок.
//  3. Пауза, чтобы TX-B гарантированно дошла до своего Get/Set.
//  4. TX-A добавляет "1" и коммитит.
//  5. join TX-B.
//  6. Все три блока ([.0], [.1], [.2]) обязаны присутствовать.
func TestIntegration_Subnet_ConcurrentAddCidr_NoLostUpdate(t *testing.T) {
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

	netID := ids.NewID(ids.PrefixNetwork)
	subID := ids.NewID(ids.PrefixSubnet)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-cidr", Name: domain.RcNameVPC("n-cidr")}); e != nil {
			return e
		}
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: subID, ProjectID: "f-cidr", Name: domain.RcNameVPC("s-cidr"),
			NetworkID: netID, PlacementType: domain.PlacementZonal, ZoneID: "zone-a",
			V4CidrBlocks: []string{"10.0.0.0/24"},
		})
		return e
	}))

	// TX-A: держим row-lock.
	wa, err := r.Writer(ctx)
	require.NoError(t, err)
	subA, err := wa.Subnets().GetForUpdate(ctx, subID)
	require.NoError(t, err)

	// TX-B в горутине: добавляет "10.0.2.0/24".
	bDone := make(chan error, 1)
	go func() {
		wb, err := r.Writer(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer wb.Abort()
		subB, err := wb.Subnets().GetForUpdate(ctx, subID) // под FOR UPDATE блокируется до commit TX-A
		if err != nil {
			bDone <- err
			return
		}
		mergedB := append(append([]string{}, subB.V4CidrBlocks...), "10.0.2.0/24")
		if _, err := wb.Subnets().SetCidrBlocks(ctx, subID, mergedB, subB.V6CidrBlocks); err != nil {
			bDone <- err
			return
		}
		bDone <- wb.Commit()
	}()

	// Дать TX-B дойти до своего Get/Set, пока TX-A держит lock.
	time.Sleep(300 * time.Millisecond)

	// TX-A: добавляет "10.0.1.0/24" и коммитит → освобождает lock.
	mergedA := append(append([]string{}, subA.V4CidrBlocks...), "10.0.1.0/24")
	_, err = wa.Subnets().SetCidrBlocks(ctx, subID, mergedA, subA.V6CidrBlocks)
	require.NoError(t, err)
	require.NoError(t, wa.Commit())

	require.NoError(t, <-bDone)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Subnets().Get(ctx, subID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24"}, got.V4CidrBlocks,
		"both concurrent AddCidrBlocks must be preserved (no lost-update)")
}
