// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"google.golang.org/grpc"

	corecfg "github.com/PRO-Robotech/kacho-corelib/config"
	"github.com/PRO-Robotech/kacho-corelib/grpcclient"
	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
)

// mtlsEnvPrefix — корневой сегмент env-имен для per-edge mTLS.
// LoadPrefixed (envconfig) выводит env-имя каждого поля из иерархии:
// mtlsEnvPrefix + tag родительского поля + tag/field примитива →
// KACHO_VPC_<EDGE>_<NAME>.
const mtlsEnvPrefix = "KACHO_VPC"

// MTLSConfig — per-edge opt-in mTLS. Загружается ОТДЕЛЬНО от основного
// viper-конфига через envconfig (LoadMTLS) — это горизонтальные corelib
// value-структуры grpcclient.TLSClient / grpcsrv.TLSServer, у которых нет
// mapstructure-тегов; envconfig их обрабатывает по полям напрямую.
//
// Каждое ребро независимо: env-имена выводятся из тега родительского поля.
// Напр. IAMRegisterMTLS → KACHO_VPC_IAM_REGISTER_MTLS_{ENABLE,CERTFILE,KEYFILE,
// CAFILES,SERVERNAME}. Enable=false (default) → insecure (dev backward-compat).
// Per-edge enable → независимый rollback.
type MTLSConfig struct {
	// IAMRegisterMTLS — client-creds для ребра vpc→iam (register-drainer →
	// InternalIAMService.RegisterResource/UnregisterResource). FGA-proxy edge.
	IAMRegisterMTLS grpcclient.TLSClient `envconfig:"IAM_REGISTER_MTLS"`

	// IAMProjectMTLS — client-creds для read-ребра vpc→iam (ProjectService.Get:
	// existence + leaf-owner-lookup на Network.Create / Address.Create). Dial-host —
	// iam **public** listener (:9090). Зеркало IAMRegisterMTLS, но свой ServerName
	// (`kacho-iam.*`, :9090 SAN — НЕ совпадает с :9091 register-ребром).
	// → KACHO_VPC_IAM_PROJECT_MTLS_{ENABLE,CERTFILE,KEYFILE,CAFILES,SERVERNAME}.
	IAMProjectMTLS grpcclient.TLSClient `envconfig:"IAM_PROJECT_MTLS"`

	// IAMAuthzMTLS — client-creds для authz-ребра vpc→iam (InternalIAMService.Check:
	// per-RPC authz-gate И project-level list-filter — оба делят ОДИН authzConn).
	// Dial-host — iam **internal** listener (:9091, Internal-only Check, ban #6).
	// ServerName `kacho-iam-internal.*` (:9091 SAN).
	// → KACHO_VPC_IAM_AUTHZ_MTLS_{ENABLE,CERTFILE,KEYFILE,CAFILES,SERVERNAME}.
	IAMAuthzMTLS grpcclient.TLSClient `envconfig:"IAM_AUTHZ_MTLS"`

	// GeoMTLS — client-creds для ребра vpc→geo (geo.v1.ZoneService.Get,
	// валидация zone_id при Subnet.Create / AddressPool.Create). Geography —
	// leaf-домен kacho-geo, ребро vpc→compute «ради zone» отсутствует.
	// → KACHO_VPC_GEO_MTLS_{ENABLE,CERTFILE,KEYFILE,CAFILES,SERVERNAME}.
	GeoMTLS grpcclient.TLSClient `envconfig:"GEO_MTLS"`

	// PublicServerMTLS — server-creds для публичного listener (:9090).
	PublicServerMTLS grpcsrv.TLSServer `envconfig:"PUBLIC_SERVER_MTLS"`

	// InternalServerMTLS — server-creds для cluster-internal listener (:9091,
	// InternalAddressService/InternalAddressPoolService/InternalNetworkService).
	InternalServerMTLS grpcsrv.TLSServer `envconfig:"INTERNAL_SERVER_MTLS"`
}

// LoadMTLS читает per-edge mTLS-конфиг из env (KACHO_VPC_*). enable=false по
// каждому ребру (zero-value) → текущее insecure-поведение (dev).
func LoadMTLS() (MTLSConfig, error) {
	var m MTLSConfig
	if err := corecfg.LoadPrefixed(mtlsEnvPrefix, &m); err != nil {
		return MTLSConfig{}, err
	}
	return m, nil
}

// IAMRegisterClientCreds возвращает grpc.DialOption для ребра vpc→iam
// (register-drainer). Enable=false → insecure (dev backward-compat); enable=true
// без валидного cert-trio → error (fail-closed, без silent insecure-fallback).
func (m MTLSConfig) IAMRegisterClientCreds() (grpc.DialOption, error) {
	return grpcclient.TLSClientCreds(m.IAMRegisterMTLS)
}

// GeoClientCreds возвращает grpc.DialOption для ребра vpc→geo (zone_id
// validation via geo.v1.ZoneService.Get).
func (m MTLSConfig) GeoClientCreds() (grpc.DialOption, error) {
	return grpcclient.TLSClientCreds(m.GeoMTLS)
}

// IAMProjectClientCreds возвращает grpc.DialOption для read-ребра vpc→iam
// (ProjectService.Get, iam public :9090). Enable=false → insecure (dev
// backward-compat); enable=true без валидного cert-trio → error (fail-closed,
// без silent insecure-fallback).
func (m MTLSConfig) IAMProjectClientCreds() (grpc.DialOption, error) {
	return grpcclient.TLSClientCreds(m.IAMProjectMTLS)
}

// IAMAuthzClientCreds возвращает grpc.DialOption для authz-ребра vpc→iam
// (InternalIAMService.Check — per-RPC gate + list-filter, iam internal :9091).
// Enable=false → insecure (dev); enable=true без валидного cert-trio → error
// (fail-closed).
func (m MTLSConfig) IAMAuthzClientCreds() (grpc.DialOption, error) {
	return grpcclient.TLSClientCreds(m.IAMAuthzMTLS)
}

// PublicServerCreds возвращает grpc.ServerOption для публичного listener (:9090).
func (m MTLSConfig) PublicServerCreds() (grpc.ServerOption, error) {
	return grpcsrv.TLSServerCreds(m.PublicServerMTLS)
}

// InternalServerCreds возвращает grpc.ServerOption для internal listener (:9091).
func (m MTLSConfig) InternalServerCreds() (grpc.ServerOption, error) {
	return grpcsrv.TLSServerCreds(m.InternalServerMTLS)
}
