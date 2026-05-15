// Package config — конфигурация kacho-vpc (skill evgeniy §8 J.1–J.7).
//
// Заменяет старый `internal/config` с envconfig-struct-tags на YAML + viper.
// Default'ы — в defaults.go (правило J.3 — не в struct-tags). ENV-binding —
// в load.go через `viper.SetEnvPrefix("KACHO_VPC")` + delimiter `__` для
// иерархии (`KACHO_VPC_REPOSITORY__POSTGRES__URL` → `repository.postgres.url`).
//
// Mode (J.6/J.7): bool productionMode → ENUM Mode{ModeDev, ModeProduction,
// ModeProductionStrict}. Имя «AuthMode» (J.7) переименовано в Mode — это
// общий режим работы (anonymous-allowed / fail-closed / fail-closed+strict-TLS),
// а не «auth-mode» (там TLS/none — отдельная подсекция authn.*).
package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Mode — общий режим работы сервиса (skill evgeniy §8 J.6/J.7).
//
//	ModeDev              — anonymous-mode разрешён (interceptor пропускает callers
//	                       без AuthN-headers как admin); insecure dev-defaults
//	                       (TLS off, sslmode=disable) только логируются.
//	ModeProduction       — fail-closed: каждый запрос обязан иметь не-пустой
//	                       TenantCtx (Actor + (Admin или FolderIDs)). Anonymous →
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
