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

// vpc8-C-04: production + breakglass=true → старт разрешен (явный аварийный обход).
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

// vpc8-C-09: production-strict с обоими server-mTLS → старт разрешен.
func TestValidateServerMTLS_ProductionStrict_BothOn_Passes(t *testing.T) {
	c := prodCfg(ModeProductionStrict, "kacho-iam:9091", false)
	var m MTLSConfig
	m.PublicServerMTLS = grpcsrv.TLSServer{Enable: true}
	m.InternalServerMTLS = grpcsrv.TLSServer{Enable: true}
	require.NoError(t, c.ValidateServerMTLS(m))
}

// vpc8-C-10: production (не strict) БЕЗ public-mTLS и БЕЗ trusted-forwarder → отказ.
// SEC-hardening r2 (2026-07-05): публичный :9090 listener выводит authz-principal'а
// из client-asserted x-kacho-* metadata; в production он не должен доверять ей по
// незашифрованному транспорту без явного подтверждения границы доверия (CWE-290).
func TestValidateServerMTLS_Production_NoMTLS_NoForwarder_Fails(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	var m MTLSConfig // оба server-mTLS выключены, trusted-forwarder не выставлен
	err := c.ValidateServerMTLS(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "public listener mTLS required")
	require.Contains(t, err.Error(), "production mode (production)")
	// SEC-hardening r6: internal :9091 — service→service, mTLS обязателен и в
	// non-strict production → его сообщение тоже всплывает.
	require.Contains(t, err.Error(), "internal listener mTLS required")
}

// vpc8-C-10b: production + public-mTLS включён (без trusted-forwarder) → старт разрешён.
func TestValidateServerMTLS_Production_PublicMTLS_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	var m MTLSConfig
	m.PublicServerMTLS.Enable = true
	// internal :9091 — service→service, mTLS обязателен в ЛЮБОМ production-режиме.
	m.InternalServerMTLS.Enable = true
	require.NoError(t, c.ValidateServerMTLS(m))
}

// vpc8-C-10c: production + trusted-forwarder=true (без public-mTLS) → старт разрешён
// (оператор явно подтвердил, что listener за аутентифицированным forwarder'ом).
func TestValidateServerMTLS_Production_TrustedForwarder_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.AuthN.TrustedForwarder = true
	var m MTLSConfig // public-mTLS выключен, но internal обязателен всегда в production
	m.InternalServerMTLS.Enable = true
	require.NoError(t, c.ValidateServerMTLS(m))
}

// vpc8-C-10f: production (non-strict) БЕЗ internal-mTLS → отказ.
// SEC-hardening r6 (2026-07-05): internal :9091 — service→service, поэтому mTLS
// обязателен в ЛЮБОМ production-режиме (security.md AuthN-инвариант: «Internal
// (:9091) НЕ освобождён: mTLS обязателен»). Раньше non-strict production запускал
// internal listener без транспортной аутентификации, доверяя client-asserted
// x-kacho-* subject на admin/IPAM поверхности (InternalAddressPoolService,
// InternalNetworkService.GetNetwork с infra vrf_id, InternalAddressService) —
// principal-spoofing (CWE-306/290). У internal НЕТ trusted-forwarder escape-hatch
// (в отличие от публичного user→edge listener'а).
func TestValidateServerMTLS_Production_NoInternalMTLS_Fails(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	var m MTLSConfig
	m.PublicServerMTLS.Enable = true    // публичный удовлетворён
	m.InternalServerMTLS.Enable = false // internal выключен → отказ
	err := c.ValidateServerMTLS(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "internal listener mTLS required")
	require.Contains(t, err.Error(), "production mode (production)")
}

// vpc8-C-10g: production (non-strict) + trusted-forwarder НЕ спасает internal —
// escape-hatch действует только для публичного listener'а.
func TestValidateServerMTLS_Production_TrustedForwarder_StillRequiresInternalMTLS(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.AuthN.TrustedForwarder = true
	var m MTLSConfig // оба выключены; trusted-forwarder закрывает только public
	err := c.ValidateServerMTLS(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "internal listener mTLS required")
	// public удовлетворён trusted-forwarder'ом — его сообщение всплыть не должно.
	require.NotContains(t, err.Error(), "public listener mTLS required")
}

// vpc8-C-10d: production-strict ИГНОРИРУЕТ trusted-forwarder — server-mTLS обязателен
// всегда (escape-hatch не действует в strict).
func TestValidateServerMTLS_ProductionStrict_TrustedForwarder_StillRequiresMTLS(t *testing.T) {
	c := prodCfg(ModeProductionStrict, "kacho-iam:9091", false)
	c.AuthN.TrustedForwarder = true
	var m MTLSConfig // оба выключены
	err := c.ValidateServerMTLS(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "public listener mTLS required")
	require.Contains(t, err.Error(), "internal listener mTLS required")
}

// vpc8-C-10e: dev-режим гардом не затронут (public-mTLS не требуется).
func TestValidateServerMTLS_Dev_NoMTLSRequired(t *testing.T) {
	c := prodCfg(ModeDev, "kacho-iam:9091", false)
	var m MTLSConfig
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

// vpc8-C-12: production (non-strict) с ssl-mode=disable → отказ (DB-трафик и пароль
// открытым текстом). SEC-hardening r2 (2026-07-05, CWE-319): защищённый sslmode
// требуется в ЛЮБОМ IsProduction() режиме, не только strict.
func TestValidate_Production_SSLModeDisable_Fails(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.Repository.Postgres.SSLMode = "disable"
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ssl-mode must be one of require|verify-ca|verify-full")
	require.Contains(t, err.Error(), "production mode (production)")
}

// vpc8-C-13: production с ssl-mode=require → проходит.
func TestValidate_Production_SSLModeRequire_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.Repository.Postgres.SSLMode = "require"
	require.NoError(t, c.Validate())
}

// vpc8-C-14: dev с ssl-mode=disable — не затронут (dev допускает plaintext).
func TestValidate_Dev_SSLModeDisable_Passes(t *testing.T) {
	var c Config
	c.AuthN.Mode = ModeDev
	c.APIServer.Endpoint = "tcp://0.0.0.0:9090"
	c.APIServer.InternalEndpoint = "tcp://0.0.0.0:9091"
	c.Repository.Postgres.URL = "postgres://u@h:5432/db"
	c.Repository.Postgres.SSLMode = "disable"
	c.Logger.Level = "INFO"
	require.NoError(t, c.Validate())
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
