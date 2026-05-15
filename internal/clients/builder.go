// Package clients — cross-service gRPC client builder (KAC-97).
//
// Этот файл — единая точка сборки gRPC-клиентских соединений из kacho-vpc к
// peer-сервисам (kacho-resource-manager, kacho-compute) согласно skill evgeniy §9 K.6:
// «`dialResourceManager` заменить на `H-BF/corlib/client/grpc/client-builder.go`
// — единый паттерн для всех gRPC-клиентов (retries, LB, TLS, metrics)».
//
// Builder — обёртка над corlib `ClientFromAddress(...)` с дефолтами kacho-vpc
// (retries=3, dialTimeout=10s, KeepAlive 30s, userAgent="kacho-vpc"). Сохраняет
// KAC-39 round_robin LB через DNSLB-флаг (corlib builder не поддерживает
// `grpc.WithDefaultServiceConfig` нативно, поэтому при DNSLB=true используется
// прямой `grpc.NewClient` с теми же defaults — single code path, общие константы).
//
// Возвращает `Conn` — interface `grpc.ClientConnInterface + io.Closer`. Generated
// proto-клиенты (`rmv1.NewFolderServiceClient(conn)` и т.п.) принимают
// `grpc.ClientConnInterface` → работают и с corlib `ClientConn`, и с
// `*grpc.ClientConn`.
package clients

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"
	"time"

	corlibgrpc "github.com/H-BF/corlib/client/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	_ "google.golang.org/grpc/resolver/dns" // регистрирует dns:/// resolver (для DNSLB)
)

// Conn — то, что нужно generated proto-клиентам (`grpc.ClientConnInterface`)
// плюс возможность Close. Подходит и corlib `ClientConn`, и `*grpc.ClientConn`.
type Conn interface {
	grpc.ClientConnInterface
	io.Closer
}

// BuildOptions — параметры сборки cross-service gRPC-клиента.
//
// Endpoint — host:port (или `dns:///host:port`, если уже с префиксом).
// TLS=true → credentials.NewTLS(MinVersion=1.2); иначе insecure (dev).
// DNSLB=true → префикс `dns:///` + service-config с round_robin LB (KAC-39).
//
// Retries / DialTimeout / KeepAliveTime — дефолты задаются через withDefaults().
type BuildOptions struct {
	Endpoint      string        // host:port (либо уже dns:///host:port)
	TLS           bool          // true → TLS 1.2+; false → insecure (dev)
	DNSLB         bool          // true → dns:///prefix + round_robin LB (KAC-39)
	Retries       uint          // gRPC retries on Unavailable (default 3)
	DialTimeout   time.Duration // dial backoff target (default 10s)
	KeepAliveTime time.Duration // ping every (default 30s)
	UserAgent     string        // gRPC User-Agent (default "kacho-vpc")
}

// defaultBuildOptions — дефолты для kacho-vpc cross-service вызовов
// (retries=3, dial 10s, keepalive 30s). Подбираются под profile peer-сервисов
// (Folder.Exists / Zone.Get — short calls, цена retry мала; idle longer для
// низкочастотных кешированных путей).
const (
	defaultRetries       = 3
	defaultDialTimeout   = 10 * time.Second
	defaultKeepAliveTime = 30 * time.Second
	defaultUserAgent     = "kacho-vpc"
)

func (o BuildOptions) withDefaults() BuildOptions {
	if o.Retries == 0 {
		o.Retries = defaultRetries
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = defaultDialTimeout
	}
	if o.KeepAliveTime == 0 {
		o.KeepAliveTime = defaultKeepAliveTime
	}
	if o.UserAgent == "" {
		o.UserAgent = defaultUserAgent
	}
	return o
}

// Build открывает gRPC-клиентское соединение по BuildOptions.
//
// Поведение по флагам:
//   - DNSLB=false (default): corlib builder с retries / dialDuration / keepalive
//     / TLS / userAgent. Стандартный паттерн для cross-service.
//   - DNSLB=true (KAC-39): `grpc.NewClient` с dns:///prefix и
//     `loadBalancingConfig: round_robin` (corlib builder serviceConfig
//     не поддерживает; собираем те же defaults вручную).
//
// Возвращает `Conn` — interface с grpc.ClientConnInterface + io.Closer.
// Подходит для передачи в generated `xxxv1.NewXxxServiceClient(conn)`.
func Build(ctx context.Context, opts BuildOptions) (Conn, error) {
	if strings.TrimSpace(opts.Endpoint) == "" {
		return nil, fmt.Errorf("clients.Build: empty Endpoint")
	}
	opts = opts.withDefaults()

	creds := buildCreds(opts.TLS)

	if opts.DNSLB {
		return buildDNSLBConn(opts, creds)
	}
	return buildCorlibConn(ctx, opts, creds)
}

// buildCreds — единый source-of-truth TLS / insecure для всех cross-service
// клиентов; TLS MinVersion=1.2 верифицирует server-сертификат по системному
// trust store (production-strict mode требует TLS, см. validateAuthMode).
func buildCreds(useTLS bool) credentials.TransportCredentials {
	if useTLS {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return insecure.NewCredentials()
}

// buildCorlibConn — default path. corlib `ClientFromAddress` собирает dial-options
// (retries on Unavailable, backoff из dialDuration, keepalive params, user-agent,
// hostname-propagator interceptor).
func buildCorlibConn(ctx context.Context, opts BuildOptions, creds credentials.TransportCredentials) (Conn, error) {
	cc, err := corlibgrpc.ClientFromAddress(opts.Endpoint).
		WithCreds(creds).
		WithDialDuration(opts.DialTimeout).
		WithMaxRetries(opts.Retries).
		WithUserAgent(opts.UserAgent).
		WithKeepAlive(keepalive.ClientParameters{
			Time:                opts.KeepAliveTime,
			Timeout:             opts.KeepAliveTime / 3, // ack within 1/3 of ping interval
			PermitWithoutStream: false,
		}).
		New(ctx)
	if err != nil {
		return nil, fmt.Errorf("clients.Build: corlib dial %q: %w", opts.Endpoint, err)
	}
	return cc, nil
}

// roundRobinServiceConfig — service-config JSON для client-side round_robin LB
// (KAC-39). Применяется с dns:///prefix: grpc сам резолвит все A/AAAA записи
// Headless Service и распределяет RPC между ними. Без этого — pick_first
// (1 backend per addr).
const roundRobinServiceConfig = `{"loadBalancingConfig":[{"round_robin":{}}]}`

// buildDNSLBConn — KAC-39 path. corlib builder не экспонирует
// `grpc.WithDefaultServiceConfig`, поэтому для DNSLB случая собираем те же
// defaults через прямой `grpc.NewClient` + serviceConfig.
//
// Префикс `dns:///` добавляется автоматически (если addr им не начинается) —
// gRPC dns resolver требует его для multi-IP резолва Headless Service.
func buildDNSLBConn(opts BuildOptions, creds credentials.TransportCredentials) (Conn, error) {
	addr := opts.Endpoint
	if !strings.HasPrefix(addr, "dns:///") {
		addr = "dns:///" + addr
	}
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithUserAgent(opts.UserAgent),
		grpc.WithDefaultServiceConfig(roundRobinServiceConfig),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                opts.KeepAliveTime,
			Timeout:             opts.KeepAliveTime / 3,
			PermitWithoutStream: false,
		}),
	}
	cc, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("clients.Build: grpc.NewClient %q (DNSLB): %w", addr, err)
	}
	return cc, nil
}
