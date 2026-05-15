package clients

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Builder unit-tests (KAC-97) — без сети.
//
// Что проверяем (skill evgeniy §9 K.6 + задача DoD):
//   - empty Endpoint → ошибка (sanity guard).
//   - TLS=false + DNSLB=false (default path) → возвращает open Conn (corlib
//     лениво устанавливает соединение, дerror только при invalid addr).
//   - DNSLB=true → addr получает префикс `dns:///` и conn открывается с
//     round_robin LB (KAC-39 preserved). Реальный resolver не дёргается до RPC,
//     поэтому Build не блокируется.
//   - withDefaults — корректно заполняет zero-valued поля.
//
// Не проверяем здесь:
//   - TLS handshake с реальным CA → требует test-сервер (это уже не unit, а
//     integration; покрывается стенд-test'ами в kacho-deploy).

func TestBuild_EmptyEndpoint(t *testing.T) {
	t.Parallel()
	_, err := Build(context.Background(), BuildOptions{Endpoint: ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty Endpoint")
}

func TestBuild_EmptyEndpoint_WhitespaceOnly(t *testing.T) {
	t.Parallel()
	_, err := Build(context.Background(), BuildOptions{Endpoint: "   "})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty Endpoint")
}

func TestBuild_Insecure_NoDNSLB(t *testing.T) {
	t.Parallel()
	// Default path: corlib builder с insecure + retries + keepalive.
	// corlib + grpc — оба resolve lazy: Build не делает actual handshake,
	// просто возвращает ClientConn в IDLE. Если addr parsable — успех.
	conn, err := Build(context.Background(), BuildOptions{
		Endpoint: "localhost:65535",
		TLS:      false,
	})
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
}

func TestBuild_DNSLB_PrependsScheme(t *testing.T) {
	t.Parallel()
	// DNSLB path: addr должен получить префикс `dns:///`. Сетевого вызова
	// нет (grpc.NewClient lazy), но мы можем проверить через invocation
	// successful + что conn открыт.
	conn, err := Build(context.Background(), BuildOptions{
		Endpoint: "resource-manager.kacho.svc.cluster.local:9090",
		DNSLB:    true,
	})
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()
	// Косвенно подтверждаем, что builder выбрал DNSLB-путь: Target()
	// (через interface assertion на *grpc.ClientConn) начинается с `dns:///`.
	type targeter interface {
		Target() string
	}
	if tg, ok := conn.(targeter); ok {
		require.True(t, strings.HasPrefix(tg.Target(), "dns:///"),
			"expected dns:/// prefix on Target(), got %q", tg.Target())
	}
}

func TestBuild_DNSLB_RespectsExistingScheme(t *testing.T) {
	t.Parallel()
	// Если addr уже с `dns:///` префиксом — не дублируем.
	conn, err := Build(context.Background(), BuildOptions{
		Endpoint: "dns:///resource-manager.kacho.svc.cluster.local:9090",
		DNSLB:    true,
	})
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()
	type targeter interface {
		Target() string
	}
	if tg, ok := conn.(targeter); ok {
		// Один префикс, не двойной (dns:///dns:///...).
		require.False(t, strings.HasPrefix(tg.Target(), "dns:///dns:///"),
			"double dns:/// prefix on Target(): %q", tg.Target())
	}
}

func TestBuildOptions_WithDefaults(t *testing.T) {
	t.Parallel()
	// Zero-valued struct → дефолты заполняются.
	opts := BuildOptions{Endpoint: "host:9090"}.withDefaults()
	require.Equal(t, uint(defaultRetries), opts.Retries)
	require.Equal(t, defaultDialTimeout, opts.DialTimeout)
	require.Equal(t, defaultKeepAliveTime, opts.KeepAliveTime)
	require.Equal(t, defaultUserAgent, opts.UserAgent)

	// Уже заполненные поля не перезаписываются.
	custom := BuildOptions{
		Endpoint:      "host:9090",
		Retries:       5,
		DialTimeout:   3 * time.Second,
		KeepAliveTime: 60 * time.Second,
		UserAgent:     "custom-agent",
	}.withDefaults()
	require.Equal(t, uint(5), custom.Retries)
	require.Equal(t, 3*time.Second, custom.DialTimeout)
	require.Equal(t, 60*time.Second, custom.KeepAliveTime)
	require.Equal(t, "custom-agent", custom.UserAgent)
}

func TestBuild_TLSEnabled_ValidEndpoint(t *testing.T) {
	t.Parallel()
	// TLS=true + insecure parsable addr — Build успешно создаёт conn (handshake
	// lazy, реальный TLS hello уйдёт на первый RPC). Это проверяет, что creds
	// constructor (credentials.NewTLS) корректно работает в нашем path и не
	// panics на пустой системный trust store / etc.
	conn, err := Build(context.Background(), BuildOptions{
		Endpoint: "localhost:65535",
		TLS:      true,
	})
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
}
