package check

import (
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc"

	"github.com/PRO-Robotech/kacho-corelib/authz"
)

// Options — параметры для NewInterceptor.
//
// IAMConn — gRPC client-conn к kacho-iam internal-port'у (обычно
// `kacho-iam.kacho.svc.cluster.local:9091`). Если nil — фабрика возвращает
// (nil, nil), и caller обязан НЕ ставить authz-interceptor в цепочку
// (graceful start без kacho-iam в dev — см. acceptance §6 D-6 fail-mode +
// scope-guard «НЕ ставить interceptor если IAM client not configured»).
//
// Breakglass — если true (через env `KACHO_VPC_AUTHZ__BREAKGLASS=true`),
// interceptor пропускает все RPC без Check + emit'ит WARN-метрику. Dev-only.
type Options struct {
	ServiceName string
	IAMConn     grpc.ClientConnInterface
	Breakglass  bool
	Logger      *slog.Logger

	// CheckTimeout — таймаут на один Check-вызов (default 2s).
	CheckTimeout time.Duration

	// DenyRateLimitPerSec — token-bucket per-Principal на denied-storm
	// (0 → отключён, default рекомендуется 100/s).
	DenyRateLimitPerSec float64

	// CacheTTL — TTL positive-results кеша (default 5s; см. acceptance D-9).
	CacheTTL time.Duration

	// AllowSystemPrincipal — system-principal (bootstrap) пропускается без
	// Check (default false). Включать для миграций / фоновых job'ов.
	AllowSystemPrincipal bool
}

// ErrIAMConnNotConfigured — IAM-conn = nil И break-glass=false. Caller'у
// нужно либо подать IAMConn, либо включить break-glass (dev).
var ErrIAMConnNotConfigured = errors.New("check: IAM connection not configured and Breakglass=false")

// NewInterceptor собирает `*authz.Interceptor` из Options. Возвращает:
//
//   - (*authz.Interceptor, nil) — успех; caller должен подвесить
//     Unary()/Stream() в цепочку interceptor'ов своего grpc.Server.
//   - (nil, ErrIAMConnNotConfigured) — IAM не сконфигурирован И break-glass=false.
//     Caller сам решает, как реагировать: в production-mode — fatal error;
//     в dev — log+continue без authz-interceptor'а.
//
// Никаких panic'ов наружу не выпускается; все invalid-options оборачиваются
// в error.
func NewInterceptor(opts Options) (*authz.Interceptor, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	// Break-glass mode: IAMConn может быть nil — interceptor всё-равно
	// нужен, чтобы emit'ить breakglass-метрики/логи.
	if opts.Breakglass {
		return authz.NewInterceptor(authz.InterceptorOptions{
			ServiceName:          opts.ServiceName,
			Map:                  PermissionMap(),
			Client:               nil, // не используется при Breakglass=true
			Cache:                authz.NewCache(opts.CacheTTL),
			Logger:               opts.Logger,
			Breakglass:           true,
			DenyRateLimitPerSec:  opts.DenyRateLimitPerSec,
			CheckTimeout:         opts.CheckTimeout,
			AllowSystemPrincipal: opts.AllowSystemPrincipal,
		}), nil
	}

	if opts.IAMConn == nil {
		return nil, ErrIAMConnNotConfigured
	}

	client := NewIAMCheckClient(opts.IAMConn)
	return authz.NewInterceptor(authz.InterceptorOptions{
		ServiceName:          opts.ServiceName,
		Map:                  PermissionMap(),
		Client:               client,
		Cache:                authz.NewCache(opts.CacheTTL),
		Logger:               opts.Logger,
		Breakglass:           false,
		DenyRateLimitPerSec:  opts.DenyRateLimitPerSec,
		CheckTimeout:         opts.CheckTimeout,
		AllowSystemPrincipal: opts.AllowSystemPrincipal,
	}), nil
}
