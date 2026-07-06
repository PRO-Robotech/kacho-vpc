// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Fail-closed boot-гардрейл S4 (SEC-hardening r9, 2026-07-06): транспортная
// аутентификация ИСХОДЯЩИХ ребер vpc→iam. До этого гарда production/production-strict
// boot был валиден, даже когда per-RPC authz Check edge (authzConn →
// InternalIAMService.Check) и/или ProjectService.Get edge дилились по cleartext gRPC:
// оба per-edge флага (mtls.IAMAuthzMTLS.Enable / authz.iam-tls.enable и
// mtls.IAMProjectMTLS.Enable / extapi.iam.tls.enable) по умолчанию false, а
// dialPeer тихо откатывался в insecure.NewCredentials(). Сетевой MITM authz-ответа мог
// подделать allowed=true → полный обход авторизации. ValidateServerMTLS энфорсит mTLS
// только на ЛИСТЕНЕРАХ; исходящие authz-рёбра оставались незащищёнными.
//
// Гард зеркалит S2 (listener-guard): в ЛЮБОМ production-режиме authz Check edge и
// ProjectService.Get edge обязаны нести verified transport (client-mTLS ЛИБО
// verified server-TLS), иначе старт отклоняется.

// prodCfgSecurePeers — production Config, у которого ОБА vpc→iam ребра защищены
// (authz Check edge через client-mTLS, ProjectService.Get edge через server-TLS
// из prodCfg). База, чтобы точечно ослаблять одно ребро в конкретном тесте.
func prodCfgSecurePeers(mode Mode, iamEndpoint string, breakglass bool) (Config, MTLSConfig) {
	c := prodCfg(mode, iamEndpoint, breakglass) // ExtAPI.IAM.TLS.Enable=true → project edge ok
	var m MTLSConfig
	m.IAMAuthzMTLS.Enable = true // authz Check edge ok (client-mTLS)
	return c, m
}

// vpc9-C-00: демонстрация ДЫРЫ, которую закрывает S4. Существующие boot-гарды
// (S1 Validate + S2 ValidateServerMTLS) ОБА пропускают production-конфиг, в котором
// исходящий authz Check edge полностью незашифрован — то есть сервис «загрузился бы».
// Именно ValidatePeerTransport теперь отклоняет такой старт.
func TestPeerTransport_GapDemonstration_S1S2Pass(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false) // ExtAPI.IAM.TLS.Enable=true
	c.Repository.Postgres.SSLMode = "verify-full"
	var m MTLSConfig
	// Листенеры защищены (S2 удовлетворён), но исходящий authz Check edge — нет.
	m.PublicServerMTLS.Enable = true
	m.InternalServerMTLS.Enable = true
	// authz Check edge: iam-tls.enable=false + IAMAuthzMTLS.Enable=false → cleartext.
	require.False(t, c.AuthZ.IAMTLS.Enable)
	require.False(t, m.IAMAuthzMTLS.Enable)
	// S1 и S2 не видят проблемы (обе валидации проходят) → boot успешен без S4.
	require.NoError(t, c.Validate(), "S1 alone does not catch the insecure authz edge")
	require.NoError(t, c.ValidateServerMTLS(m), "S2 guards listeners only, not the outbound authz edge")
	// S4 отклоняет.
	require.Error(t, c.ValidatePeerTransport(m))
}

// vpc9-C-01: production + authz Check edge cleartext (нет ни mTLS, ни server-TLS) → отказ.
func TestValidatePeerTransport_Production_AuthzEdgeInsecure_Fails(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false) // project edge ok (server-TLS)
	var m MTLSConfig                                      // IAMAuthzMTLS off; c.AuthZ.IAMTLS off
	err := c.ValidatePeerTransport(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "authz Check edge")
	require.Contains(t, err.Error(), "production mode (production)")
	// project edge удовлетворён (extapi.iam.tls.enable=true) → его сообщение не всплывает.
	require.NotContains(t, err.Error(), "ProjectService.Get edge")
}

// vpc9-C-02: production-strict + authz Check edge cleartext → тот же отказ (любой IsProduction()).
func TestValidatePeerTransport_ProductionStrict_AuthzEdgeInsecure_Fails(t *testing.T) {
	c := prodCfg(ModeProductionStrict, "kacho-iam:9091", false)
	var m MTLSConfig
	err := c.ValidatePeerTransport(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "authz Check edge")
	require.Contains(t, err.Error(), "production mode (production-strict)")
}

// vpc9-C-03: production + authz Check edge через client-mTLS → проходит.
func TestValidatePeerTransport_Production_AuthzEdgeMTLS_Passes(t *testing.T) {
	c, m := prodCfgSecurePeers(ModeProduction, "kacho-iam:9091", false)
	require.True(t, m.IAMAuthzMTLS.Enable)
	require.NoError(t, c.ValidatePeerTransport(m))
}

// vpc9-C-04: production + authz Check edge через verified server-TLS (authz.iam-tls.enable)
// → проходит даже без client-mTLS.
func TestValidatePeerTransport_Production_AuthzEdgeServerTLS_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.AuthZ.IAMTLS.Enable = true // verified server-TLS вместо mTLS
	var m MTLSConfig             // client-mTLS выключен
	require.NoError(t, c.ValidatePeerTransport(m))
}

// vpc9-C-05: production + breakglass=true → authz Check edge ОСВОБОЖДЁН (Check не
// выполняется, ребро не несёт security-решения — mirror S1 breakglass-escape). Старт
// разрешён даже при cleartext authz-ребре, если project edge защищён.
func TestValidatePeerTransport_Production_Breakglass_AuthzEdgeExempt(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", true) // breakglass=true, project edge ok
	var m MTLSConfig                                     // authz edge cleartext
	require.NoError(t, c.ValidatePeerTransport(m))
}

// vpc9-C-05b: production + breakglass=true + пустой authz-endpoint → authz-ребро не
// дилится вовсе → нет требования (project edge остаётся под гардом).
func TestValidatePeerTransport_Production_Breakglass_NoEndpoint_AuthzEdgeExempt(t *testing.T) {
	c := prodCfg(ModeProduction, "", true)
	var m MTLSConfig
	m.IAMProjectMTLS.Enable = true // project edge ok, authz edge неактивен
	require.NoError(t, c.ValidatePeerTransport(m))
}

// vpc9-C-06: production + ProjectService.Get edge cleartext (extapi.iam.tls.enable=false,
// IAMProjectMTLS выключен) → отказ (тот же класс гарда). authz edge изолирован через mTLS.
func TestValidatePeerTransport_Production_ProjectEdgeInsecure_Fails(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.ExtAPI.IAM.TLS.Enable = false // project edge cleartext
	var m MTLSConfig
	m.IAMAuthzMTLS.Enable = true // authz edge удовлетворён → изолируем project
	err := c.ValidatePeerTransport(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ProjectService.Get edge")
	require.Contains(t, err.Error(), "production mode (production)")
	require.NotContains(t, err.Error(), "authz Check edge")
}

// vpc9-C-07: production + ProjectService.Get edge через client-mTLS (server-TLS off) → проходит.
func TestValidatePeerTransport_Production_ProjectEdgeMTLS_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.ExtAPI.IAM.TLS.Enable = false
	var m MTLSConfig
	m.IAMAuthzMTLS.Enable = true   // authz edge ok
	m.IAMProjectMTLS.Enable = true // project edge ok через mTLS
	require.NoError(t, c.ValidatePeerTransport(m))
}

// vpc9-C-08: production + ProjectService.Get edge через verified server-TLS → проходит.
func TestValidatePeerTransport_Production_ProjectEdgeServerTLS_Passes(t *testing.T) {
	c, m := prodCfgSecurePeers(ModeProduction, "kacho-iam:9091", false) // ExtAPI.IAM.TLS.Enable=true
	require.True(t, c.ExtAPI.IAM.TLS.Enable)
	require.NoError(t, c.ValidatePeerTransport(m))
}

// vpc9-C-09: production + ОБА ребра cleartext → оба сообщения агрегируются в один multierr.
func TestValidatePeerTransport_Production_BothEdgesInsecure_AggregatesBoth(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	c.ExtAPI.IAM.TLS.Enable = false // project edge cleartext
	var m MTLSConfig                // authz edge cleartext
	err := c.ValidatePeerTransport(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "authz Check edge")
	require.Contains(t, err.Error(), "ProjectService.Get edge")
}

// vpc9-C-10: dev-режим гардом не затронут (исходящие рёбра могут быть insecure).
func TestValidatePeerTransport_Dev_NoGuard(t *testing.T) {
	c := prodCfg(ModeDev, "kacho-iam:9091", false)
	c.ExtAPI.IAM.TLS.Enable = false
	var m MTLSConfig // всё insecure
	require.NoError(t, c.ValidatePeerTransport(m))
}

// vpc9-C-11: ValidateBoot агрегирует S4 — insecure authz edge в production всплывает
// в едином boot-валидаторе (single-shot gate).
func TestValidateBoot_Production_IncludesPeerTransport(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false)
	var m MTLSConfig
	m.PublicServerMTLS.Enable = true   // S2 public ok
	m.InternalServerMTLS.Enable = true // S2 internal ok
	// authz Check edge остаётся cleartext (IAMAuthzMTLS off, authz.iam-tls off).
	err := c.ValidateBoot(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "authz Check edge")
}

// vpc9-C-12: ValidateBoot зелёный, когда всё (листенеры + исходящие рёбра) защищено.
func TestValidateBoot_Production_AllSecure_Passes(t *testing.T) {
	c := prodCfg(ModeProduction, "kacho-iam:9091", false) // project edge server-TLS
	c.Repository.Postgres.SSLMode = "verify-full"
	var m MTLSConfig
	m.PublicServerMTLS.Enable = true
	m.InternalServerMTLS.Enable = true
	m.IAMAuthzMTLS.Enable = true // authz Check edge ok
	require.NoError(t, c.ValidateBoot(m))
}
