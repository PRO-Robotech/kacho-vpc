// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"fmt"
	"strings"

	"go.uber.org/multierr"
)

// Frozen-тексты boot-гардрейлов. Это часть контракта (наблюдаемый отказ старта),
// меняются только осознанно. %s подставляет Mode.String() (production|production-strict).
const (
	errAuthzEndpointRequired = "production mode (%s): authz.iam-endpoint is required " +
		"(set the kacho-iam internal endpoint, or authz.breakglass=true to bypass authz)"
	errPublicMTLSRequired = "production mode (%s): public listener mTLS required " +
		"(set KACHO_VPC_PUBLIC_SERVER_MTLS_ENABLE=true with cert/key/ca) — the public :9090 " +
		"listener derives the authorization principal from client-asserted x-kacho-* metadata; " +
		"without verified transport auth any direct caller can spoof an arbitrary principal " +
		"(cross-tenant authz bypass). If the listener sits behind an authenticated " +
		"forwarder/service-mesh that terminates client identity, set authn.trusted-forwarder=true " +
		"to acknowledge that trust boundary (production-strict ignores this escape hatch)"
	errInternalMTLSRequired = "production-strict mode: internal listener mTLS required " +
		"(set KACHO_VPC_INTERNAL_SERVER_MTLS_ENABLE=true with cert/key/ca)"

	// S3-гардрейлы (list-filter обязателен для ScopeFiltered RPC в production).
	// %s = Mode.String(); %d = число ScopeFiltered RPC; %s = их имена через ", ".
	errListFilterRequiredForScopeFiltered = "production mode (%s): authz.list-filter.enabled=true is required " +
		"— %d RPC(s) are ScopeFiltered (%s) and rely on the data-level list-filter for object-scope " +
		"authorization; with the filter off their authz degrades to header-trusted ownership " +
		"(cross-project enumeration). Enable authz.list-filter or drop ScopeFiltered from the permission map"
	errListFilterEndpointRequired = "production mode (%s): authz.list-filter.enabled=true but no resolvable " +
		"authorize/iam endpoint (authz.list-filter.authorize-endpoint and authz.iam-endpoint both empty) " +
		"— the filter degrades to passthrough (unfiltered), leaving %d ScopeFiltered RPC(s) fail-open; " +
		"set authz.list-filter.authorize-endpoint (or authz.iam-endpoint)"

	// WarnBreakglassProduction — громкое предупреждение boot'а, когда authz целиком
	// обойден в production (emergency-обход). Логируется composition root'ом на WARN.
	WarnBreakglassProduction = "authz.breakglass=true in production mode (%s): " +
		"ALL authz Check is BYPASSED — every RPC is allowed without IAM authorization; emergency use only"
)

// Validate проверяет инварианты Config — чистая функция без побочных эффектов и без
// логгера. Возвращает multierr со ВСЕМИ найденными проблемами сразу, а именно:
//   - authn.mode — известное значение ENUM;
//   - logger.level — известный уровень;
//   - listen-endpoint'ы парсятся в адрес;
//   - ssl-mode из допустимого набора;
//   - в production (любом) требуется authz.iam-endpoint либо явный authz.breakglass;
//   - в production-strict дополнительно требуется extapi.iam.tls.enable и защищенный ssl-mode.
//
// Fail-closed boot-гардрейл (S1): secure-by-default (`authn.mode=production`)
// подтверждается ОТКАЗОМ старта при невалидной prod-конфигурации, а не тихим
// небезопасным запуском. server-mTLS (S2) проверяется отдельно через
// ValidateServerMTLS (MTLSConfig грузится вне viper-Config).
func (c Config) Validate() error {
	var errs error

	errs = multierr.Append(errs, c.validateMode())

	if _, err := ParseLogLevel(c.Logger.Level); err != nil {
		errs = multierr.Append(errs, err)
	}

	if listenAddress(c.APIServer.Endpoint) == "" {
		errs = multierr.Append(errs,
			fmt.Errorf("api-server.endpoint is empty"))
	}
	if listenAddress(c.APIServer.InternalEndpoint) == "" {
		errs = multierr.Append(errs,
			fmt.Errorf("api-server.internal-endpoint is empty"))
	}

	switch strings.ToLower(c.Repository.Postgres.SSLMode) {
	case "disable", "require", "verify-ca", "verify-full":
	case "":
		// допускаем — baseDSN подставит "disable"
	default:
		errs = multierr.Append(errs,
			fmt.Errorf("repository.postgres.ssl-mode=%q (allowed: disable, require, verify-ca, verify-full)",
				c.Repository.Postgres.SSLMode))
	}

	if strings.TrimSpace(c.Repository.Postgres.URL) == "" {
		errs = multierr.Append(errs,
			fmt.Errorf("repository.postgres.url is empty"))
	}

	// S1: production (любой вариант) обязан нести сконфигурированный authz-эндпоинт
	// либо явный break-glass. Без authz-Check production-инстанс принял бы
	// подделанную x-kacho-* metadata как полноправного админа — обход авторизации.
	if c.AuthN.Mode.IsProduction() &&
		strings.TrimSpace(c.AuthZ.IAMEndpoint) == "" &&
		!c.AuthZ.Breakglass {
		errs = multierr.Append(errs,
			fmt.Errorf(errAuthzEndpointRequired, c.AuthN.Mode))
	}

	// S1b: защищённый DB-транспорт требуется в ЛЮБОМ production-режиме, не только
	// strict (CWE-319). ssl-mode=disable в production → пароль KACHO_VPC_DB_PASSWORD
	// и весь query-трафик идут открытым текстом; sniffer в DB-сегменте перехватывает
	// креды. dev допускает disable (plaintext локально).
	if c.AuthN.Mode.IsProduction() {
		switch strings.ToLower(c.Repository.Postgres.SSLMode) {
		case "require", "verify-ca", "verify-full":
			// OK
		default:
			errs = multierr.Append(errs,
				fmt.Errorf("production mode (%s): repository.postgres.ssl-mode must be one of require|verify-ca|verify-full (got %q)",
					c.AuthN.Mode, c.Repository.Postgres.SSLMode))
		}
	}

	if c.AuthN.Mode == ModeProductionStrict {
		if !c.ExtAPI.IAM.TLS.Enable {
			errs = multierr.Append(errs,
				fmt.Errorf("production-strict mode: extapi.iam.tls.enable=true required"))
		}
	}

	return errs
}

// ValidateServerMTLS — boot-гардрейл S2: транспортная аутентификация публичного
// (:9090) и internal (:9091) листенеров. MTLSConfig грузится отдельно от
// viper-Config (envconfig, LoadMTLS), поэтому проверка — отдельный метод,
// вызываемый сразу после config.LoadMTLS() и ДО net.Listen.
//
// Публичный :9090 listener выводит authz-principal'а из client-asserted x-kacho-*
// metadata. В ЛЮБОМ production-режиме доверять этой metadata по незашифрованному
// транспорту запрещено (CWE-290 spoofing): иначе прямой вызов :9090 подделывает
// произвольного principal'а и обходит tenant-изоляцию. Поэтому:
//   - production-strict — server-mTLS обязателен на ОБОИХ листенерах, без
//     исключений (trusted-forwarder-флаг игнорируется).
//   - production (non-strict) — публичный listener требует ЛИБО PublicServerMTLS,
//     ЛИБО явного authn.trusted-forwarder=true (оператор подтверждает, что :9090
//     стоит за аутентифицированным forwarder'ом/mesh, который сам терминирует
//     идентичность клиента); internal listener mTLS в non-strict не обязателен.
//   - dev — требований нет.
//
// Возвращает multierr со всеми нарушениями сразу.
func (c Config) ValidateServerMTLS(m MTLSConfig) error {
	if !c.AuthN.Mode.IsProduction() {
		return nil
	}
	var errs error

	// Публичный listener: server-mTLS ИЛИ (только в non-strict) trusted-forwarder ack.
	publicAuthenticated := m.PublicServerMTLS.Enable ||
		(c.AuthN.Mode == ModeProduction && c.AuthN.TrustedForwarder)
	if !publicAuthenticated {
		errs = multierr.Append(errs, fmt.Errorf(errPublicMTLSRequired, c.AuthN.Mode))
	}

	// Internal listener: обязателен только в strict (cluster-internal :9091).
	if c.AuthN.Mode == ModeProductionStrict && !m.InternalServerMTLS.Enable {
		errs = multierr.Append(errs, fmt.Errorf("%s", errInternalMTLSRequired))
	}
	return errs
}

// ValidateListFilter — boot-гардрейл S3: если permission-map несёт хотя бы один
// ScopeFiltered RPC, его object-scope авторизация возлагается на data-level
// list-filter (authz-interceptor отдаёт для ScopeFiltered DecisionInternal и
// пропускает per-RPC Check). В production фильтр ОБЯЗАН быть включён и иметь
// резолвимый authorize/iam эндпоинт — иначе авторизация деградирует до
// header-trusted AssertProjectOwnership (cross-project enumeration).
//
// scopeFilteredRPCs — имена ScopeFiltered методов из permission-map. Передаются
// composition root'ом (check.ScopeFilteredRPCs()), чтобы config НЕ импортировал
// пакет check — Validate остаётся чистой и без import-цикла. Пустой список
// (текущее состояние карты после SEC-фикса 2026-07-05) → guard no-op.
//
// Fail-closed: закрывает "helm default fail-open" residual — stock production
// install с values.yaml default (list-filter.enabled=false) при наличии
// ScopeFiltered RPC теперь ОТКАЗЫВАЕТ старт, а не логирует WARN и тихо запускается
// в degraded state. dev-режим гардом не затронут (может гонять unfiltered).
func (c Config) ValidateListFilter(scopeFilteredRPCs []string) error {
	if len(scopeFilteredRPCs) == 0 || !c.AuthN.Mode.IsProduction() {
		return nil
	}
	if !c.AuthZ.ListFilter.Enabled {
		return fmt.Errorf(errListFilterRequiredForScopeFiltered,
			c.AuthN.Mode, len(scopeFilteredRPCs), strings.Join(scopeFilteredRPCs, ", "))
	}
	// Enabled, но без резолвимого endpoint'а → buildListFilter даёт passthrough
	// (conn==nil, WARN + nil-фильтр) — тот же fail-open. Отказываем.
	if strings.TrimSpace(c.AuthZ.ListFilter.AuthorizeEndpoint) == "" &&
		strings.TrimSpace(c.AuthZ.IAMEndpoint) == "" {
		return fmt.Errorf(errListFilterEndpointRequired, c.AuthN.Mode, len(scopeFilteredRPCs))
	}
	return nil
}

// ValidateBoot — единый boot-валидатор: агрегирует Validate (S1 + базовые
// инварианты) и ValidateServerMTLS (S2) в один multierr, чтобы оператор увидел
// полный список проблем за один прогон. Используется как single-shot gate перед
// привязкой листенеров.
//
// S3 (ValidateListFilter) НЕ входит сюда: ему нужен список ScopeFiltered RPC из
// permission-map (пакет check), который config не импортирует — его вызывает
// composition root отдельно (см. cmd/vpc/main.go).
func (c Config) ValidateBoot(m MTLSConfig) error {
	return multierr.Append(c.Validate(), c.ValidateServerMTLS(m))
}

// validateMode гарантирует, что Mode — известное значение (ENUM).
func (c Config) validateMode() error {
	switch c.AuthN.Mode {
	case ModeDev, ModeProduction, ModeProductionStrict:
		return nil
	default:
		return fmt.Errorf("authn.mode invalid (got %s)", c.AuthN.Mode)
	}
}

// InsecureDevWarnings возвращает список «не блокирующих» предупреждений
// о небезопасных dev-defaults. В production-режиме возвращает nil.
func (c Config) InsecureDevWarnings() []string {
	if c.AuthN.Mode.IsProduction() {
		return nil
	}
	var out []string
	if !c.ExtAPI.IAM.TLS.Enable {
		out = append(out,
			"extapi.iam.tls.enable=false — cross-service gRPC plaintext (dev only)")
	}
	mode := strings.ToLower(c.Repository.Postgres.SSLMode)
	if mode == "" || mode == "disable" {
		out = append(out,
			"repository.postgres.ssl-mode=disable — DB plaintext (dev only)")
	}
	return out
}
