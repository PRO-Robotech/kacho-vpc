// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

import "testing"

// firstViolation — достать первое нарушение из доменной *ValidationError.
func firstViolation(t *testing.T, err error) FieldViolation {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
	}
	if len(ve.Violations) == 0 {
		t.Fatalf("ValidationError has no violations")
	}
	return ve.Violations[0]
}

func validPool() AddressPool {
	return AddressPool{
		ID:               "apl-x",
		Name:             "pool-ext-a",
		Description:      "prod external",
		Labels:           RcLabels{"tier": "prod"},
		SelectorLabels:   RcLabels{"tier": "prod"},
		SelectorPriority: 100,
		Kind:             AddressPoolKindExternalPublic,
		V4CIDRBlocks:     []string{"203.0.113.0/24"},
	}
}

// vpc8G-B2 — happy path.
func TestAddressPool_vpc8G_B2_Validate_OK(t *testing.T) {
	if err := validPool().Validate(); err != nil {
		t.Fatalf("valid pool must pass Validate, got %v", err)
	}
}

// vpc8G-B3 — невалидный name.
func TestAddressPool_vpc8G_B3_Validate_BadName(t *testing.T) {
	p := validPool()
	p.Name = "1bad name!"
	v := firstViolation(t, p.Validate())
	if v.Field != "name" {
		t.Fatalf("field = %q, want name", v.Field)
	}
	const want = `name must match ^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$ (letters, digits, hyphens, underscores; starts with letter; up to 63 chars; empty allowed)`
	if v.Msg != want {
		t.Fatalf("msg = %q, want %q", v.Msg, want)
	}
}

// vpc8G-B4 — description > 256.
func TestAddressPool_vpc8G_B4_Validate_DescriptionTooLong(t *testing.T) {
	p := validPool()
	long := make([]rune, 257)
	for i := range long {
		long[i] = 'x'
	}
	p.Description = RcDescription(string(long))
	v := firstViolation(t, p.Validate())
	if v.Field != "description" || v.Msg != "description length exceeds 256 chars" {
		t.Fatalf("got field=%q msg=%q", v.Field, v.Msg)
	}
}

// vpc8G-B5 — невалидные labels / selector_labels.
func TestAddressPool_vpc8G_B5_Validate_BadLabels(t *testing.T) {
	t.Run("uppercase label key → labels.BadKey", func(t *testing.T) {
		p := validPool()
		p.Labels = RcLabels{"BadKey": "v"}
		v := firstViolation(t, p.Validate())
		if v.Field != "labels.BadKey" {
			t.Fatalf("field = %q, want labels.BadKey", v.Field)
		}
	})
	t.Run("too many labels", func(t *testing.T) {
		p := validPool()
		lbls := RcLabels{}
		for i := 0; i < 65; i++ {
			lbls[LabelKey("k"+string(rune('a'+i%26))+string(rune('a'+i/26)))] = "v"
		}
		p.Labels = lbls
		v := firstViolation(t, p.Validate())
		if v.Field != "labels" || v.Msg != "too many labels (max 64)" {
			t.Fatalf("got field=%q msg=%q", v.Field, v.Msg)
		}
	})
	t.Run("selector_labels uppercase key reported under labels.Tier (frozen)", func(t *testing.T) {
		p := validPool()
		p.SelectorLabels = RcLabels{"Tier": "x"}
		v := firstViolation(t, p.Validate())
		if v.Field != "labels.Tier" {
			t.Fatalf("field = %q, want labels.Tier (frozen)", v.Field)
		}
	})
}

// vpc8G-B6 — отрицательный selector_priority; границы.
func TestAddressPool_vpc8G_B6_Validate_SelectorPriority(t *testing.T) {
	t.Run("negative rejected", func(t *testing.T) {
		p := validPool()
		p.SelectorPriority = -1
		v := firstViolation(t, p.Validate())
		if v.Field != "selector_priority" || v.Msg != "selector_priority must be non-negative" {
			t.Fatalf("got field=%q msg=%q", v.Field, v.Msg)
		}
	})
	t.Run("zero and int32 max accepted", func(t *testing.T) {
		for _, prio := range []int32{0, 2147483647} {
			p := validPool()
			p.SelectorPriority = prio
			if err := p.Validate(); err != nil {
				t.Fatalf("selector_priority %d must pass, got %v", prio, err)
			}
		}
	})
}

// vpc8G-B7 — неизвестный kind.
func TestAddressPool_vpc8G_B7_Validate_UnknownKind(t *testing.T) {
	p := validPool()
	p.Kind = AddressPoolKind(2)
	v := firstViolation(t, p.Validate())
	if v.Field != "kind" || v.Msg != "unknown address pool kind" {
		t.Fatalf("got field=%q msg=%q", v.Field, v.Msg)
	}
}
