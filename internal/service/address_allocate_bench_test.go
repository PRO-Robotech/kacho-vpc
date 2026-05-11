package service

import (
	"net/netip"
	"testing"
)

// BenchmarkPickRandomIPv4_24 — hot-path allocator: random IP в /24.
// Запуск:
//
//	go test -bench=BenchmarkPickRandomIPv4 -benchmem ./internal/service/
//
// Цель: убедиться что crypto/rand + binary не доминирует над allocate-loop'ом.
func BenchmarkPickRandomIPv4_24(b *testing.B) {
	cidr := netip.MustParsePrefix("198.51.100.0/24")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pickRandomIPv4(cidr)
	}
}

// BenchmarkPickRandomIPv4_30 — small CIDR (2 usable IPs).
func BenchmarkPickRandomIPv4_30(b *testing.B) {
	cidr := netip.MustParsePrefix("203.0.113.0/30")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pickRandomIPv4(cidr)
	}
}

// BenchmarkUsableIPv4Count — utility для GetUtilization.
func BenchmarkUsableIPv4Count(b *testing.B) {
	cidrs := []string{"198.51.100.0/24", "203.0.113.0/30", "192.0.2.0/16"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, c := range cidrs {
			_ = usableIPv4Count(c)
		}
	}
}

// BenchmarkIsUniqueViolation — error inspection в retry-loop.
// Должен быть дешёвым; иначе retry становится bottleneck.
func BenchmarkIsUniqueViolation(b *testing.B) {
	err := ErrAlreadyExists
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isUniqueViolation(err)
	}
}

// BenchmarkUsableIPv4Sweep — sweep enumeration для allocator Phase 2
// (deterministic fallback после random-phase). На /28 (14 IP) полный sweep —
// max 24 IP по cap'у, 0-allocation expected на каждый IP кроме результата.
func BenchmarkUsableIPv4Sweep(b *testing.B) {
	cidr := netip.MustParsePrefix("198.51.100.0/28")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = usableIPv4Sweep(cidr, 24)
	}
}
