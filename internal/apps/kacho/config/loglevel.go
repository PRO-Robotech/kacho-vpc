// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config

import (
	"fmt"
	"log/slog"
	"strings"
)

// levelFatal — порог выше Error для FATAL-строк. slog не имеет встроенного
// FATAL, поэтому маппим его на Error+4 (тот же шаг, что между стандартными
// уровнями slog).
const levelFatal = slog.LevelError + 4

// ParseLogLevel переводит конфиг-строку logger.level (FATAL|ERROR|WARN|INFO|DEBUG,
// case-insensitive) в slog.Level. Неизвестное значение → ошибка (config-mistake
// виден сразу, без тихого fallback в INFO).
func ParseLogLevel(s string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug, nil
	case "INFO", "": // пустая строка — back-compat дефолт INFO
		return slog.LevelInfo, nil
	case "WARN", "WARNING":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	case "FATAL":
		return levelFatal, nil
	default:
		return slog.LevelInfo, fmt.Errorf("logger.level=%q invalid (allowed: FATAL, ERROR, WARN, INFO, DEBUG)", s)
	}
}

// SlogLevel возвращает распарсенный уровень логгера. Ошибка парсинга
// перехватывается Validate на старте, поэтому здесь при невалидном уровне
// возвращается INFO как безопасный дефолт (этот путь недостижим после Validate).
func (c Config) SlogLevel() slog.Level {
	lvl, err := ParseLogLevel(c.Logger.Level)
	if err != nil {
		return slog.LevelInfo
	}
	return lvl
}
