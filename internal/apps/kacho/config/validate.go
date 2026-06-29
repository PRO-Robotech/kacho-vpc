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
	errPublicMTLSRequired = "production-strict mode: public listener mTLS required " +
		"(set KACHO_VPC_PUBLIC_SERVER_MTLS_ENABLE=true with cert/key/ca)"
	errInternalMTLSRequired = "production-strict mode: internal listener mTLS required " +
		"(set KACHO_VPC_INTERNAL_SERVER_MTLS_ENABLE=true with cert/key/ca)"

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

	if c.AuthN.Mode == ModeProductionStrict {
		if !c.ExtAPI.IAM.TLS.Enable {
			errs = multierr.Append(errs,
				fmt.Errorf("production-strict mode: extapi.iam.tls.enable=true required"))
		}
		switch strings.ToLower(c.Repository.Postgres.SSLMode) {
		case "require", "verify-ca", "verify-full":
			// OK
		default:
			errs = multierr.Append(errs,
				fmt.Errorf("production-strict mode: repository.postgres.ssl-mode must be one of require|verify-ca|verify-full (got %q)",
					c.Repository.Postgres.SSLMode))
		}
	}

	return errs
}

// ValidateServerMTLS — boot-гардрейл S2: production-strict требует включенный
// server-mTLS на ОБОИХ листенерах (public :9090 + internal :9091). MTLSConfig
// грузится отдельно от viper-Config (envconfig, LoadMTLS), поэтому проверка —
// отдельный метод, вызываемый сразу после config.LoadMTLS() и ДО net.Listen.
//
// Для не-strict режимов (dev / production) server-mTLS не обязателен — это
// эксклюзивное требование production-strict (fail-closed + строго-проверенный
// транспорт). Возвращает multierr со всеми нарушениями сразу.
func (c Config) ValidateServerMTLS(m MTLSConfig) error {
	if c.AuthN.Mode != ModeProductionStrict {
		return nil
	}
	var errs error
	if !m.PublicServerMTLS.Enable {
		errs = multierr.Append(errs, fmt.Errorf("%s", errPublicMTLSRequired))
	}
	if !m.InternalServerMTLS.Enable {
		errs = multierr.Append(errs, fmt.Errorf("%s", errInternalMTLSRequired))
	}
	return errs
}

// ValidateBoot — единый boot-валидатор: агрегирует Validate (S1 + базовые
// инварианты) и ValidateServerMTLS (S2) в один multierr, чтобы оператор увидел
// полный список проблем за один прогон. Используется как single-shot gate перед
// привязкой листенеров.
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
