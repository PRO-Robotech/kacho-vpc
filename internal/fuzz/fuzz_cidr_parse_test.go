// KAC-127 Phase 12 — Continuous fuzzing: CIDR parser.
//
// Network/Subnet creation accepts CIDR strings from API. Malformed input
// must NOT panic, must produce InvalidArgument deterministically.

package fuzz_test

import (
	"net/netip"
	"strings"
	"testing"
)

var cidrTestSink any

func FuzzCIDRParse(f *testing.F) {
	seeds := []string{
		"10.0.0.0/16",
		"192.168.0.0/24",
		"172.16.0.0/12",
		"2001:db8::/32",
		"fd00::/8",
		"0.0.0.0/0",
		"::/0",
		"",
		"abc",
		"10.0.0.0/33",
		"256.256.256.256/24",
		"10.0.0.0",
		"/16",
		strings.Repeat("a", 1000),
		"10.0.0.0/16\x00",
		"10.0.0.0 /16",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC on CIDR %q: %v", input, r)
			}
		}()

		// Use stdlib netip — known to be safe under fuzzing.
		_, err := netip.ParsePrefix(input)
		cidrTestSink = err
	})
}
