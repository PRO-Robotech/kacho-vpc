// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package macutil — генерация MAC-адресов для NIC'ов Kachō. Helper не привязан
// ни к одному use-case'у; используется из `internal/apps/kacho/api/networkinterface`.
package macutil

import (
	"crypto/rand"
	"fmt"
)

// kachoMACPrefix — фиксированный первый байт MAC-адресов Kachō NIC'ов:
// `0x0e` (binary `0000 1110` — locally administered + unicast, lsb=0).
// Все Kachō-MAC начинаются с `0e:` — по этому префиксу (в tcpdump / логах)
// видно, что MAC выдан нашим control-plane'ом, а не назначен сторонним runtime'ом.
const kachoMACPrefix byte = 0x0e

// macRandomBytes — сколько байт энтропии добавляется к префиксу, чтобы получить
// полный 6-октетный MAC. 5 байт = 40 бит ≈ 1T значений; вероятность коллизии
// при 1M NIC'ов в облаке порядка 1e-3 — ловится UNIQUE-constraint'ом в БД +
// retry на стороне CreateNetworkInterfaceUseCase.
const macRandomBytes = 5

// GenerateMAC возвращает свежий MAC-адрес для NIC. Формат — lowercase,
// colon-separated, всегда 6 октетов; первый октет — `0e` (Kachō prefix),
// остальные 5 — `crypto/rand`. Пример: `0e:1a:2b:3c:4d:5e`.
//
// Ошибка возвращается только если `crypto/rand.Read` упал (отказ источника
// энтропии ОС — в нормальной работе не случается).
func GenerateMAC() (string, error) {
	var b [macRandomBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		kachoMACPrefix, b[0], b[1], b[2], b[3], b[4]), nil
}
