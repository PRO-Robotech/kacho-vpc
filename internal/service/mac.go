package service

import (
	"crypto/rand"
	"fmt"
)

// kachoMACPrefix — фиксированный первый байт MAC-адресов Kachō NIC'ов:
// `0x0e` (binary `0000 1110` — locally administered + unicast, lsb=0).
// Все Kachō-MAC начинаются с `0e:` — это видно при tcpdump / в логах, что
// MAC родом из нашего control-plane'а, а не назначен runtime'ом (libvirt,
// QEMU, Cilium, Multus и т.д.).
const kachoMACPrefix byte = 0x0e

// macRandomBytes — сколько байт энтропии добавляется к префиксу, чтобы получить
// полный 6-октетный MAC. 5 байт = 40 бит ≈ 1T значений; вероятность коллизии
// при 1M NIC'ов в облаке порядка 1e-3 — ловится UNIQUE-constraint'ом в БД +
// retry на стороне NetworkInterfaceService.doCreate.
const macRandomBytes = 5

// GenerateMAC возвращает свежий MAC-адрес для NIC. Формат — lowercase,
// colon-separated, всегда 6 октетов; первый октет — `0e` (Kachō prefix),
// остальные 5 — `crypto/rand`. Пример: `0e:1a:2b:3c:4d:5e`.
//
// Ошибка возвращается только если `crypto/rand.Read` упал (catastrophic
// state ОС — не должно случаться в нормальной работе).
func GenerateMAC() (string, error) {
	var b [macRandomBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		kachoMACPrefix, b[0], b[1], b[2], b[3], b[4]), nil
}
