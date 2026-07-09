// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package config — конфигурация kacho-vpc (YAML + viper).
//
// Default'ы — в defaults.go (не в struct-tags). ENV-binding — в load.go через
// `viper.SetEnvPrefix("KACHO_VPC")` + delimiter `__` для иерархии
// (`KACHO_VPC_REPOSITORY__POSTGRES__URL` → `repository.postgres.url`).
//
// Mode — общий режим работы сервиса (anonymous-allowed / fail-closed /
// fail-closed+strict-TLS), ENUM Mode{ModeDev, ModeProduction,
// ModeProductionStrict}. Это не «auth-mode» (TLS/none — отдельная подсекция
// authn.*).
package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Mode — общий режим работы сервиса.
//
//	ModeDev              — СТРОГО локальный fixture-режим (unit/integration-тесты и
//	                       локальная разработка), НИКОГДА не для развернутого стенда:
//	                       interceptor пропускает callers без AuthN-headers как admin,
//	                       insecure-defaults (TLS off, sslmode=disable) только
//	                       логируются. В проде — ModeProduction (fail-closed).
//	ModeProduction       — fail-closed: каждый запрос обязан иметь не-пустой
//	                       TenantCtx (Admin или ProjectIDs). Anonymous →
//	                       PermissionDenied.
//	ModeProductionStrict — production + дополнительно валидирует extapi.*.tls.*
//	                       и repository.postgres.ssl-mode (require|verify-ca|verify-full).
type Mode int

// Значения ENUM. iota порядок стабилен; не менять без миграции values.yaml.
const (
	ModeDev Mode = iota
	ModeProduction
	ModeProductionStrict
)

// String — каноническое имя для логирования / config-ошибок.
func (m Mode) String() string {
	switch m {
	case ModeDev:
		return "dev"
	case ModeProduction:
		return "production"
	case ModeProductionStrict:
		return "production-strict"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// IsProduction возвращает true для любого production-варианта.
func (m Mode) IsProduction() bool {
	return m == ModeProduction || m == ModeProductionStrict
}

// parseMode — точечная инверсия String(); используется кастомным
// mapstructure-хуком и YAML-/ENV-loader'ом.
func parseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dev":
		return ModeDev, nil
	case "production":
		return ModeProduction, nil
	case "production-strict":
		return ModeProductionStrict, nil
	default:
		return ModeDev, fmt.Errorf("unknown mode %q (allowed: dev, production, production-strict)", s)
	}
}

// MarshalJSON / UnmarshalJSON — для удобной сериализации (mapstructure
// сам через DecodeHook парсит string, но JSON-output логов и тестов
// удобнее иметь строкой).
func (m Mode) MarshalJSON() ([]byte, error) { return json.Marshal(m.String()) }

func (m *Mode) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := parseMode(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
