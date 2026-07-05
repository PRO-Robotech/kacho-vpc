// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/authz"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// TestScopeFilteredRPCs_MatchesMap — ScopeFilteredRPCs() обязан вернуть ровно тот
// набор методов, у которых RPCEntry.ScopeFiltered=true в PermissionMap, и в
// детерминированном (отсортированном) порядке. Композит-рут скармливает этот
// список config.ValidateListFilter (S3 boot-guard).
func TestScopeFilteredRPCs_MatchesMap(t *testing.T) {
	got := check.ScopeFilteredRPCs()

	var want []string
	for full, e := range check.PermissionMap() {
		if e.ScopeFiltered {
			want = append(want, full)
		}
	}
	sort.Strings(want)

	require.Equal(t, want, got)
	require.True(t, sort.StringsAreSorted(got), "результат детерминирован (отсортирован)")
}

// TestScopeFilteredRPCs_CurrentlyEmpty — фиксирует текущее состояние карты после
// SEC-фикса 2026-07-05: НИ ОДИН RPC не ScopeFiltered (NetworkService/List снят с
// ScopeFiltered). Если кто-то вернёт ScopeFiltered — этот guard подсветит, что
// production-boot теперь требует list-filter (S3).
func TestScopeFilteredRPCs_CurrentlyEmpty(t *testing.T) {
	require.Empty(t, check.ScopeFilteredRPCs())
}

// TestScopeFilteredRPCs_DetectsScopeFiltered — при наличии ScopeFiltered entry
// helper его находит (проверяем на локальной карте, не мутируя PermissionMap).
func TestScopeFilteredRPCs_DetectsScopeFiltered(t *testing.T) {
	m := authz.RPCMap{
		"/svc/A": {ScopeFiltered: true},
		"/svc/B": {ScopeFiltered: false},
		"/svc/C": {ScopeFiltered: true},
	}
	require.Equal(t, []string{"/svc/A", "/svc/C"}, check.ScopeFilteredRPCsOf(m))
}
