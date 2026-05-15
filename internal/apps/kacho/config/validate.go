package config

import (
	"fmt"
	"strings"

	"go.uber.org/multierr"
)

// Validate проверяет инварианты Config (skill evgeniy §8 J.5 — без передачи
// logger; чистая функция без побочных эффектов).
//
// Возвращает multierr, который содержит ВСЕ найденные проблемы сразу.
//
// Перенос из старого cmd/vpc/main.go::validateAuthMode + расширения:
//   - Mode-ENUM (раньше string switch).
//   - production-strict дополнительно валидирует extapi.*.tls.enable + ssl-mode.
//   - listen-endpoint'ы должны парситься в адрес.
func (c Config) Validate() error {
	var errs error

	errs = multierr.Append(errs, c.validateMode())

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

	if c.Watch.MaxStreams <= 0 {
		errs = multierr.Append(errs,
			fmt.Errorf("watch.max-streams must be > 0 (got %d)", c.Watch.MaxStreams))
	}

	if c.AuthN.Mode == ModeProductionStrict {
		if !c.ExtAPI.ResourceManager.TLS.Enable {
			errs = multierr.Append(errs,
				fmt.Errorf("production-strict mode: extapi.resource-manager.tls.enable=true required"))
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
	if !c.ExtAPI.ResourceManager.TLS.Enable {
		out = append(out,
			"extapi.resource-manager.tls.enable=false — cross-service gRPC plaintext (dev only)")
	}
	mode := strings.ToLower(c.Repository.Postgres.SSLMode)
	if mode == "" || mode == "disable" {
		out = append(out,
			"repository.postgres.ssl-mode=disable — DB plaintext (dev only)")
	}
	return out
}
