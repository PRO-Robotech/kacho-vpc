package vpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestSDKDialOpts_NoOpts_DefaultsKeepalive — KA-03: NewClient(addr) без явных
// opts → conn собран с keepalive-дефолтом (Time=10s, Timeout=Time/3,
// PermitWithoutStream=false — SDK-conn для активного использования) + insecure creds.
func TestSDKDialOpts_NoOpts_DefaultsKeepalive(t *testing.T) {
	p := sdkKeepalive()
	require.Equal(t, 10*time.Second, p.Time)
	require.Equal(t, 10*time.Second/3, p.Timeout)
	require.False(t, p.PermitWithoutStream, "SDK conn is active-use → PermitWithoutStream=false")

	opts := sdkDialOpts()
	// keepalive + insecure creds (dev default) = минимум 2 опции
	require.GreaterOrEqual(t, len(opts), 2,
		"no-opts NewClient must default to keepalive + insecure creds")
}

// TestSDKDialOpts_WithCallerOpts_KeepaliveStillPrepended — KA-03 and: если
// вызывающий передал свои opts (напр. TLS creds), keepalive-дефолт всё равно
// применяется (prepended), а caller-opts идут ПОСЛЕ → caller может переопределить
// keepalive своим WithKeepaliveParams (last-wins). insecure-дефолт НЕ навязывается.
func TestSDKDialOpts_WithCallerOpts_KeepaliveStillPrepended(t *testing.T) {
	callerCreds := grpc.WithTransportCredentials(insecure.NewCredentials())
	opts := sdkDialOpts(callerCreds)
	// keepalive(prepended) + callerCreds = >=2; caller-opts сохранены (длина растёт с каждым)
	require.GreaterOrEqual(t, len(opts), 2)
	require.Greater(t, len(sdkDialOpts(callerCreds, callerCreds)), len(sdkDialOpts(callerCreds)),
		"caller opts must all be preserved (appended after keepalive default)")
}
