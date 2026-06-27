// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
)

// Fail-closed prod-guardrail: secure-by-default (`authn.mode=production`) обязан
// подтверждаться отказом старта при невалидной prod-конфигурации, а не тихим
// небезопасным запуском. Тесты покрывают S1 (authz-endpoint required) и S2
// (production-strict требует server-mTLS на обоих листенерах).

// prodCfg — минимально-валидный production Config (URL/listen заданы), с
// настраиваемыми authz-полями.
func prodCfg(mode Mode, iamEndpoint string, breakglass bool) Config {
	var c Config
	c.AuthN.Mode = mode
	c.APIServer.Endpoint = "tcp://0.0.0.0:9090"
	c.APIServer.InternalEndpoint = "tcp://0.0.0.0:9091"
	c.Repository.Postgres.URL = "postgres://u@h:5432/db"
	c.Repository.Postgres.SSLMode = "verify-full"
	c.Logger.Level = "INFO"
	c.AuthZ.IAMEndpoint = iamEndpoint
	c.AuthZ.Breakglass = breakglass
	// strict-смежные инварианты удовлетворены, чтобы изолировать проверяемый гард.
	c.ExtAPI.IAM.TLS.Enable = true
	return c
}

// vpc8-C-01: production с настроенным authz-endpoint проходит Validate.
func TestValidate_Production_WithAuthzEndpoint_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam.kacho.svc.cluster.local:9091", false)
	require.NoError(t, c.Validate())
}

// vpc8-C-02: production без authz-endpoint и без breakglass → отказ.
func TestValidate_Production_NoAuthzEndpoint_Fails(t *testing.T) {
	c := prodCfg(ModeProduction, "", false)
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "authz.iam-endpoint is required")
	require.Contains(t, err.Error(), "production mode (production)")
}

// vpc8-C-03: production-strict без authz-endpoint → тот же отказ (любой IsProduction()).
func TestValidate_ProductionStrict_NoAuthzEndpoint_Fails(t *testing.T) {
	c := prodCfg(ModeProductionStrict, "", false)
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "authz.iam-endpoint is required")
	require.Contains(t, err.Error(), "production mode (production-strict)")
}

// vpc8-C-04: production + breakglass=true → старт разрешён (явный аварийный обход).
func TestValidate_Production_Breakglass_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "", true)
	require.NoError(t, c.Validate())
}

// vpc8-C-05: dev-режим гардрейлом не затронут.
func TestValidate_Dev_NoGuardrail(t *testing.T) {
	var c Config
	c.AuthN.Mode = ModeDev
	c.APIServer.Endpoint = "tcp://0.0.0.0:9090"
	c.APIServer.InternalEndpoint = "tcp://0.0.0.0:9091"
	c.Repository.Postgres.URL = "postgres://u@h:5432/db"
	c.Repository.Postgres.SSLMode = "disable"
	c.Logger.Level = "INFO"
	require.NoError(t, c.Validate())
	require.NotContains(t, errString(c.Validate()), "authz.iam-endpoint is required")
}

// vpc8-C-07: production-strict без public-mTLS → отказ (ValidateServerMTLS).
func TestValidateServerMTLS_ProductionStrict_RequiresPublicMTLS(t *testing.T) {
	c := prodCfg(ModeProductionStrict, "kacho-iam:9091", false)
	var m MTLSConfig
	m.InternalServerMTLS.Enable = true
	m.PublicServerMTLS.Enable = false
	err := c.ValidateServerMTLS(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "public listener mTLS required")
}

// vpc8-C-08: production-strict без internal-mTLS → отказ.
func TestValidateServerMTLS_ProductionStrict_RequiresInternalMTLS(t *testing.T) {
	c := prodCfg(ModeProductionStrict, "kacho-iam:9091", false)
	var m MTLSConfig
	m.PublicServerMTLS.Enable = true
	m.InternalServerMTLS.Enable = false
	err := c.ValidateServerMTLS(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "internal listener mTLS required")
}

// vpc8-C-09: production-strict с обоими server-mTLS → старт разрешён.
func TestValidateServerMTLS_ProductionStrict_BothOn_Passes(t *testing.T) {
	c := prodCfg(ModeProductionStrict, "kacho-iam:9091", false)
	var m MTLSConfig
	m.PublicServerMTLS = grpcsrv.TLSServer{Enable: true}
	m.InternalServerMTLS = grpcsrv.TLSServer{Enable: true}
	require.NoError(t, c.ValidateServerMTLS(m))
}

// vpc8-C-10: production (не strict) НЕ требует server-mTLS (граница).
func TestValidateServerMTLS_Production_NotStrict_NoMTLSRequired(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	var m MTLSConfig // оба server-mTLS выключены
	require.NoError(t, c.ValidateServerMTLS(m))
}

// vpc8-C-11: множественные нарушения strict агрегируются в один multierr
// (single boot-validation через ValidateBoot).
func TestValidateBoot_ProductionStrict_AggregatesAllViolations(t *testing.T) {
	var c Config
	c.AuthN.Mode = ModeProductionStrict
	c.APIServer.Endpoint = "tcp://0.0.0.0:9090"
	c.APIServer.InternalEndpoint = "tcp://0.0.0.0:9091"
	c.Repository.Postgres.URL = "postgres://u@h:5432/db"
	c.Repository.Postgres.SSLMode = "disable"
	c.Logger.Level = "INFO"
	c.AuthZ.IAMEndpoint = ""
	c.ExtAPI.IAM.TLS.Enable = false
	var m MTLSConfig // оба server-mTLS выключены

	err := c.ValidateBoot(m)
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "authz.iam-endpoint is required")
	require.Contains(t, msg, "extapi.iam.tls.enable=true required")
	require.Contains(t, msg, "ssl-mode must be one of require|verify-ca|verify-full")
	require.Contains(t, msg, "public listener mTLS required")
	require.Contains(t, msg, "internal listener mTLS required")
}

// H-D3: невалидный logger.level → ошибка валидации при старте (fail-fast,
// без тихого fallback в INFO).
func TestValidate_InvalidLoggerLevel_Fails(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.Logger.Level = "LOUD"
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "logger.level")
}

// H-D1/H-D2: ParseLogLevel переводит конфиг-строку в slog.Level (уважение порога).
func TestParseLogLevel_KnownLevels(t *testing.T) {
	cases := map[string]bool{"DEBUG": true, "info": true, "Warn": true, "ERROR": true, "FATAL": true, "loud": false}
	for in, ok := range cases {
		_, err := ParseLogLevel(in)
		if ok {
			require.NoError(t, err, "level %q must parse", in)
		} else {
			require.Error(t, err, "level %q must be rejected", in)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
