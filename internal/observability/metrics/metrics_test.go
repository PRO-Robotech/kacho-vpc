// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	opmetrics "github.com/PRO-Robotech/kacho-corelib/operations"
	outboxmetrics "github.com/PRO-Robotech/kacho-corelib/outbox/metrics"

	"github.com/PRO-Robotech/kacho-vpc/internal/observability/metrics"
)

// Метрики-adapter: приватный Prometheus-реестр, реализующий corelib
// operations.Recorder + outbox/metrics.Recorder, плюс dependency_up зеркало и
// build_info. Prometheus импортируется только здесь (adapter-граница).

// scrape возвращает тело /metrics приватного реестра adapter'а.
func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	return rr.Body.String()
}

// Compile-time: adapter удовлетворяет обоим corelib Recorder-портам.
var (
	_ opmetrics.Recorder     = (*metrics.Metrics)(nil)
	_ outboxmetrics.Recorder = (*metrics.Metrics)(nil)
)

func TestMetrics_BuildInfoAndRuntimeCollectors(t *testing.T) {
	m := metrics.New("1.2.3", "deadbeef")
	body := scrape(t, m)
	require.Contains(t, body, `kacho_vpc_build_info{`)
	require.Contains(t, body, `version="1.2.3"`)
	require.Contains(t, body, `commit="deadbeef"`)
	require.Contains(t, body, "go_goroutines")
}

func TestMetrics_OperationsRecorder_Exported(t *testing.T) {
	m := metrics.New("t", "t")
	m.IncReconcileRuns()
	m.IncReconcileRuns()
	m.IncOrphansRecovered("done")
	body := scrape(t, m)
	require.Contains(t, body, "kacho_vpc_operations_reconcile_runs_total 2")
	require.Contains(t, body, `kacho_vpc_operations_orphans_recovered_total{outcome="done"} 1`)
}

func TestMetrics_OutboxRecorder_Exported(t *testing.T) {
	m := metrics.New("t", "t")
	const table = "kacho_vpc.fga_register_outbox"
	m.SetBacklogDepth(table, 5)
	m.SetOldestPendingAgeSeconds(table, 12)
	m.IncPoisoned(table)
	body := scrape(t, m)
	require.Contains(t, body, `kacho_vpc_outbox_backlog_depth{table="kacho_vpc.fga_register_outbox"} 5`)
	require.Contains(t, body, `kacho_vpc_outbox_oldest_pending_age_seconds{table="kacho_vpc.fga_register_outbox"} 12`)
	require.Contains(t, body, `kacho_vpc_outbox_poisoned_total{table="kacho_vpc.fga_register_outbox"} 1`)
}

func TestMetrics_DependencyUp_Mirror(t *testing.T) {
	m := metrics.New("t", "t")
	m.SetDependencyUp("database", true)
	m.SetDependencyUp("iam-authz", false)
	body := scrape(t, m)
	require.Contains(t, body, `kacho_vpc_dependency_up{dependency="database"} 1`)
	require.Contains(t, body, `kacho_vpc_dependency_up{dependency="iam-authz"} 0`)
}

// Приватный реестр → повторная конструкция в одном процессе не паникует
// duplicate-register (тесты герметичны).
func TestMetrics_PrivateRegistry_NoDuplicatePanic(t *testing.T) {
	require.NotPanics(t, func() {
		_ = metrics.New("a", "1")
		_ = metrics.New("b", "2")
	})
}
