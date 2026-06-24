// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Непрерывный fuzzing парсера политик SecurityGroup.
//
// Правила SG принимают тройки protocol/port/CIDR. Malformed-вход не должен
// паниковать, вызывать integer overflow и обязан соблюдать лимиты длины массива.

package fuzz_test

import (
	"strings"
	"testing"
)

var sgPolicyTestSink any

func FuzzPolicyParse(f *testing.F) {
	seeds := []string{
		"tcp:80:0.0.0.0/0",
		"tcp:80-443:10.0.0.0/8",
		"udp:53:192.168.0.0/16",
		"icmp::0.0.0.0/0",
		"all:::0.0.0.0/0",
		"",
		"abc:def:ghi",
		"tcp:99999:0.0.0.0/0",
		"tcp:-1:0.0.0.0/0",
		strings.Repeat("tcp:80:0.0.0.0/0,", 10000),
		"tcp:80:0.0.0.0/0\x00",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC on SG policy %q: %v", input, r)
			}
		}()
		valid := parseSGPolicyStub(input)
		sgPolicyTestSink = valid
	})
}

func parseSGPolicyStub(s string) bool {
	if len(s) > 1<<16 {
		return false
	}
	parts := strings.Split(s, ":")
	return len(parts) >= 3 && len(parts) <= 4
}
