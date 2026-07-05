// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"net/netip"
	"strings"
)

// ErrNotIPv4 — попытка выполнить IPv4-only операцию над не-IPv4 префиксом.
// Domain-sentinel (domain импортирует только stdlib); use-case-слой при
// необходимости мапит его в transport-ошибку. Совпадает по смыслу с
// repo.ErrInvalidIPv4, но живёт в domain, где и обитает CIDR-математика.
var ErrNotIPv4 = errors.New("not ipv4")

// intToUint32 — clamp int→uint32 без паники (отрицательное → 0, переполнение →
// MaxUint32). Локальная копия corelib/safeconv.IntToUint32: domain-слой не
// импортирует corelib.
func intToUint32(v int) uint32 {
	if v < 0 {
		return 0
	}
	if int64(v) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// UsableIPv4Sweep — детерминированное перечисление usable-IPv4 в CIDR (без
// network/broadcast). Используется allocator'ом как closure-гарантия, когда
// random-pick не сходится. Cap'ируется maxN, чтобы не аллокировать миллионы
// строк для больших CIDR; для /28 (14 IP) maxN=24 достаточно.
//
//   - hostBits 0 (/32): единственный адрес.
//   - hostBits 1 (/31): оба адреса (RFC 3021 point-to-point).
//   - hostBits ≥2: пропускаем .0 (network) и .last (broadcast).
func UsableIPv4Sweep(cidr netip.Prefix, maxN int) []string {
	if !cidr.Addr().Is4() {
		return nil
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	if hostBits >= 32 {
		return nil
	}
	total := uint32(1) << hostBits
	first := uint32(1)
	last := total - 1
	switch hostBits {
	case 0:
		first, last = 0, 1
	case 1:
		first, last = 0, 2
	}
	if intToUint32(maxN) < last-first {
		last = first + intToUint32(maxN)
	}
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	out := make([]string, 0, last-first)
	for i := first; i < last; i++ {
		var ipBytes [4]byte
		binary.BigEndian.PutUint32(ipBytes[:], baseInt+i)
		out = append(out, net.IP(ipBytes[:]).String())
	}
	return out
}

// PickRandomIPv4 выбирает random IP из CIDR, исключая network/broadcast-адреса
// (для prefix length < 31). Использует crypto/rand для unpredictable allocation.
//
// Edge cases:
//   - /32 (hostBits=0): единственный адрес — base.
//   - /31 (hostBits=1): оба адреса валидны (point-to-point) — base+0 или base+1.
//   - /≤30 (hostBits≥2): пропускаем .0 (network) и .last (broadcast) →
//     offset в [1, maxHosts].
//
// Не-IPv4 префикс → ErrNotIPv4.
func PickRandomIPv4(cidr netip.Prefix) (string, error) {
	if !cidr.Addr().Is4() {
		return "", ErrNotIPv4
	}
	bits := cidr.Bits()
	hostBits := 32 - bits
	base := cidr.Addr().As4()
	baseInt := binary.BigEndian.Uint32(base[:])
	var offset uint32
	switch hostBits {
	case 0:
		return cidr.Addr().String(), nil
	case 1:
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return "", err
		}
		offset = binary.BigEndian.Uint32(randBytes[:]) % 2
	default:
		maxHosts := uint32(1<<hostBits) - 2
		var randBytes [4]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return "", err
		}
		offset = binary.BigEndian.Uint32(randBytes[:])%maxHosts + 1
	}
	var ipBytes [4]byte
	binary.BigEndian.PutUint32(ipBytes[:], baseInt+offset)
	return net.IP(ipBytes[:]).String(), nil
}

// PickRandomIPv6 выбирает случайный адрес внутри IPv6-префикса, заполняя
// host-биты криптослучайными значениями. Пропускает all-zeros host (subnet-router
// anycast `<prefix>::`); для очень узких префиксов (/127, /128) ведёт себя
// детерминированно (там почти нет выбора).
func PickRandomIPv6(prefix netip.Prefix) (string, error) {
	addr := prefix.Masked().Addr()
	base := addr.As16()
	bits := prefix.Bits()
	hostBits := 128 - bits
	if hostBits <= 0 {
		return addr.String(), nil
	}
	var rnd [16]byte
	for try := 0; try < 8; try++ {
		if _, err := rand.Read(rnd[:]); err != nil {
			return "", err
		}
		out := base
		for i := 0; i < 16; i++ {
			bitIndex := i * 8
			if bitIndex+8 <= bits {
				continue
			}
			var mask byte
			if bitIndex >= bits {
				mask = 0xff
			} else {
				keep := bits - bitIndex
				mask = byte(0xff >> keep)
			}
			out[i] = (base[i] &^ mask) | (rnd[i] & mask)
		}
		cand := netip.AddrFrom16(out)
		if cand == addr {
			continue
		}
		return cand.String(), nil
	}
	return addr.String(), nil
}

// UsableIPv4Count — usable IPs в CIDR (исключая network+broadcast).
// Для /N: 2^(32-N) - 2; для /31: 2 (RFC 3021); для /32: 1.
// Если CIDR невалиден или не IPv4 — 0.
func UsableIPv4Count(cidr string) int64 {
	p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil || !p.Addr().Is4() {
		return 0
	}
	bits := p.Bits()
	if bits == 32 {
		return 1
	}
	if bits == 31 {
		return 2
	}
	hostBits := 32 - bits
	if hostBits >= 31 {
		return 0
	}
	return int64(1)<<hostBits - 2
}
