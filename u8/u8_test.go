package u8

import (
	"testing"

	"ontology/scalar"
)

func decodeAll(t *testing.T, p []byte) []Unit {
	t.Helper()
	d := &Decoder{}
	us, _ := d.Feed(p)
	if u, ok := d.Flush(); ok {
		us = append(us, u)
	}
	return us
}

func TestInvalidUnits(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []Kind
	}{
		{"window3", []byte{0xF0, 0x90, 0x80, 0x41}, []Kind{Invalid, OK}},
		{"e0", []byte{0xE0, 0x80, 0x80}, []Kind{Invalid, Invalid, Invalid}},
		{"ed-surrogate", []byte{0xED, 0xA0, 0x80}, []Kind{Invalid, Invalid, Invalid}},
		{"c0", []byte{0xC0, 0xAF}, []Kind{Invalid, Invalid}},
		{"f4", []byte{0xF4, 0x90, 0x80, 0x80}, []Kind{Invalid, Invalid, Invalid, Invalid}},
		{"cont", []byte{0x80, 0x80}, []Kind{Invalid, Invalid}},
		{"e2-tail", []byte{0xE2, 0x82}, []Kind{Trunc}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			us := decodeAll(t, c.in)
			if len(us) != len(c.want) {
				t.Fatalf("got %d units %+v, want %d", len(us), us, len(c.want))
			}
			for i := range us {
				if us[i].Kind != c.want[i] {
					t.Fatalf("unit %d kind=%d want %d", i, us[i].Kind, c.want[i])
				}
			}
		})
	}
}

func TestValidRoundTrip(t *testing.T) {
	runes := []scalar.Rune{0, 0x41, 0x7F, 0x80, 0x7FF, 0x800, 0xFFFF, 0x10000, 0x10FFFF,
		0xD7FF, 0xE000, 0x1000, 0xFFFF, scalar.Replacement}
	var in []byte
	for _, r := range runes {
		in = Encode(in, r)
	}
	us := decodeAll(t, in)
	var out []byte
	for _, u := range us {
		if u.Kind != OK {
			t.Fatalf("unexpected %+v", u)
		}
		out = Encode(out, u.R)
	}
	if string(out) != string(in) {
		t.Fatalf("round trip mismatch")
	}
}

func TestBoundaries(t *testing.T) {
	runes := []scalar.Rune{0x41, 0xA9, 0x20AC, 0x1F600, scalar.Replacement, 0x10FFFF}
	var in []byte
	for _, r := range runes {
		in = Encode(in, r)
	}
	base := decodeAll(t, in)
	for cut := 0; cut <= len(in); cut++ {
		d := &Decoder{}
		a, _ := d.Feed(in[:cut])
		b, _ := d.Feed(in[cut:])
		if u, ok := d.Flush(); ok {
			b = append(b, u)
		}
		got := append(a, b...)
		if len(got) != len(base) {
			t.Fatalf("cut %d: %d units want %d", cut, len(got), len(base))
		}
		for i := range got {
			if got[i] != base[i] {
				t.Fatalf("cut %d unit %d: %+v want %+v", cut, i, got[i], base[i])
			}
		}
	}
}

func TestChecksBound(t *testing.T) {
	// 任意切分，每字节检查不超过 2 次。
	in := []byte{0xE0, 0x80, 0x80, 0xF0, 0x90, 0x80, 0x41, 0x80, 0xC3, 0xA9, 0xF4, 0x90}
	one := &Decoder{}
	one.Feed(in)
	if c := one.Checks(); c > 2*int64(len(in)) {
		t.Fatalf("whole checks %d > %d", c, 2*len(in))
	}
	by1 := &Decoder{}
	for _, x := range in {
		by1.Feed([]byte{x})
	}
	if by1.Checks() != one.Checks() {
		t.Fatalf("checks differ: byte=%d whole=%d", by1.Checks(), one.Checks())
	}
}
