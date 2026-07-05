// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package main

// Тесты wiring composition-root: проверяют enable/disable/no-conn контракт
// per-object FGA-фильтра (`buildListFilter` поверх AuthorizeService.ListObjects), а
// статический guard ниже держит каждый vpc→iam dial протянутым через client-cert mTLS.

import (
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/config"
)

// TestLROWorker_ReadyAfterBootWiring — composition root проводит package-level
// default-registry LRO-worker'а (ConfigureDefault + Start) ДО приема трафика:
// readiness-чекер lro-worker зеленый без единой мутации. Без этой проводки под в
// k8s залипал бы NotReady (нет трафика → нет Run → dispatcher лениво не стартует →
// NotReady навсегда — boot-deadlock).
func TestLROWorker_ReadyAfterBootWiring(t *testing.T) {
	require.False(t, operations.Ready(), "до boot-wiring default-registry dispatcher не запущен")
	require.NoError(t, startLROWorker(operations.NewMemRecorder(), discardLogger()))
	require.True(t, operations.Ready(),
		"после boot-wiring operations.Ready()=true — readiness lro-worker зеленый до трафика")
}

// dialLoopback — возвращает closed-loop grpc-conn (живой сервер не нужен:
// buildListFilter только конструирует FGAFilter поверх conn, но не вызывает его).
func dialLoopback(t *testing.T) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() })
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestBuildListFilter_Disabled_ReturnsNil(t *testing.T) {
	var cfg config.Config
	cfg.AuthZ.ListFilter.Enabled = false
	f := buildListFilter(cfg, dialLoopback(t), discardLogger())
	require.Nil(t, f, "list-filter disabled → nil (passthrough)")
}

func TestBuildListFilter_EnabledNilConn_ReturnsNil(t *testing.T) {
	// enabled, но нет authorize conn → деградация в passthrough (nil), а НЕ жесткая
	// ошибка старта.
	var cfg config.Config
	cfg.AuthZ.ListFilter.Enabled = true
	f := buildListFilter(cfg, nil, discardLogger())
	require.Nil(t, f, "enabled but nil conn → nil (passthrough + warn)")
}

func TestBuildListFilter_EnabledWithConn_ReturnsFilter(t *testing.T) {
	var cfg config.Config
	cfg.AuthZ.ListFilter.Enabled = true
	f := buildListFilter(cfg, dialLoopback(t), discardLogger())
	require.NotNil(t, f, "enabled + conn → FGA per-object filter")
}

// TestSECI_CompletenessGuard_EveryIAMDialThreadsClientCreds — статический
// completeness-gate. Composition root ОБЯЗАН протянуть per-edge client-cert mTLS
// creds в КАЖДЫЙ vpc→iam dial: public ProjectService.Get conn (`iamConn`) и internal
// InternalIAMService.Check conn (`authzConn`, общий с list-filter). Если любой
// read/authz iam-dial оставить server-auth-only/plaintext, то, когда kacho-iam
// требует и проверяет client-cert, handshake падает — guard запрещает эту регрессию,
// проверяя, что оба dial'а консультируются с mTLS-хелперами.
//
// Все четыре peer-dial'а идут через общий хелпер `dialPeer`, которому per-edge
// creds-функция передается значением (`mtlsCfg.IAM*ClientCreds`, без вызова на
// call-site); сам вызов `credsFn()` — внутри `dialPeer`. Guard проверяет и то, что
// creds-хелперы протянуты в каждый edge, и то, что `dialPeer` их действительно
// вызывает (иначе creds были бы «протянуты, но не предъявлены»).
func TestSECI_CompletenessGuard_EveryIAMDialThreadsClientCreds(t *testing.T) {
	src, err := os.ReadFile("main.go")
	require.NoError(t, err)
	main := string(src)

	for _, want := range []string{
		// Ребро ProjectService.Get (iamConn) консультируется с IAM-project mTLS-хелпером.
		"mtlsCfg.IAMProjectMTLS.Enable",
		"mtlsCfg.IAMProjectClientCreds",
		// Ребро Check + list-filter (authzConn) консультируется с IAM-authz mTLS-хелпером.
		"mtlsCfg.IAMAuthzMTLS.Enable",
		"mtlsCfg.IAMAuthzClientCreds",
		// dialPeer действительно предъявляет переданную creds-функцию (вызывает её).
		"creds, err := credsFn()",
	} {
		require.Contains(t, main, want,
			"composition root must thread client-cert mTLS into every vpc→iam read/authz dial; missing %q", want)
	}

	// Ребро register-drainer остается на mTLS через свой хелпер — без регрессии.
	require.Contains(t, main, "IAMRegisterClientCreds()",
		"register-drainer edge must keep its mTLS helper")

	// Защита от старого server-auth-only bool-пути на read/authz dial'ах: ни iamConn,
	// ни authzConn не должны дилить только с `TLS: ...IAM.TLS.Enable` /
	// `TLS: ...IAMTLS.Enable`, когда соответствующее mTLS-ребро включено. Проверяем,
	// что creds-хелпер текстуально предшествует bool-TLS-пути iamConn edge'а.
	require.Less(t,
		strings.Index(main, "mtlsCfg.IAMProjectClientCreds"),
		strings.LastIndex(main, "iamPeer.TLS.Enable"),
		"IAMProjectClientCreds mTLS branch must guard the iamConn insecure/server-auth fallback")
}
