// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package macutil

import (
	"regexp"
	"strings"
	"testing"
)

// macFormat — формат MAC, который должен выдавать GenerateMAC: 6 lowercase-hex
// октетов, разделенных двоеточиями.
var macFormat = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

func TestGenerateMAC_FormatAndPrefix(t *testing.T) {
	mac, err := GenerateMAC()
	if err != nil {
		t.Fatalf("GenerateMAC: %v", err)
	}
	if !macFormat.MatchString(mac) {
		t.Fatalf("MAC %q не соответствует формату %v", mac, macFormat)
	}
	if !strings.HasPrefix(mac, "0e:") {
		t.Fatalf("MAC %q не имеет префикса 0e: (Kachō prefix)", mac)
	}
	if len(mac) != 17 {
		t.Fatalf("MAC %q длиной %d, ожидается 17 символов", mac, len(mac))
	}
}

func TestGenerateMAC_Uniqueness(t *testing.T) {
	// 5 байт энтропии = 2^40 ≈ 1T значений; на сэмпле в 1000 коллизий быть не должно.
	const samples = 1000
	seen := make(map[string]struct{}, samples)
	for i := 0; i < samples; i++ {
		mac, err := GenerateMAC()
		if err != nil {
			t.Fatalf("GenerateMAC[%d]: %v", i, err)
		}
		if _, dup := seen[mac]; dup {
			t.Fatalf("MAC %q повторился на %d-й итерации — энтропия не работает", mac, i)
		}
		seen[mac] = struct{}{}
	}
}
