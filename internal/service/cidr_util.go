package service

import (
	"net/netip"
)

// netipPrefix — internal alias для netip.Prefix чтобы избежать прямого
// импорта пакета netip в subnet.go (избежать дублирования imports).
type netipPrefix = netip.Prefix

// parseNetipPrefix парсит CIDR-строку в netip.Prefix.
func parseNetipPrefix(s string) (netipPrefix, error) {
	return netip.ParsePrefix(s)
}

// prefixesOverlap возвращает true если два CIDR-блока пересекаются.
// Контейнерное правило: a содержит первый IP b или b содержит первый IP a.
func prefixesOverlap(a, b netipPrefix) bool {
	if a.Addr().Is4() != b.Addr().Is4() {
		// разные семейства не пересекаются
		return false
	}
	// netip.Prefix.Contains работает только с одиночным адресом, поэтому
	// проверяем границы: один содержит начало другого, либо наоборот.
	if a.Contains(b.Addr()) || b.Contains(a.Addr()) {
		return true
	}
	return false
}
