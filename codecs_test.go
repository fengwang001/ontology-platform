package ontology_test

import (
	"testing"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

func TestScalar(t *testing.T) {
	cases := []struct {
		r      rune
		scalar bool
		hi     bool
		lo     bool
	}{
		{0, true, false, false},
		{0xE000, true, false, false},
		{0xD7FF, true, false, false},
		{0xD800, false, true, false},
		{0xDBFF, false, true, false},
		{0xDC00, false, false, true},
		{0xDFFF, false, false, true},
		{0x10FFFF, true, false, false},
		{0x110000, false, false, false},
		{-1, false, false, false},
	}
	for _, c := range cases {
		if got := scalar.IsScalar(c.r); got != c.scalar {
			t.Errorf("IsScalar(%U)=%v want %v", c.r, got, c.scalar)
		}
		if scalar.IsHighSurrogate(c.r) != c.hi || scalar.IsLowSurrogate(c.r) != c.lo {
			t.Errorf("surrogate class(%U)", c.r)
		}
	}
	hi, lo, ok := scalar.SplitSurrogate(0x10437)
	if !ok || hi != 0xD801 || lo != 0xDC37 {
		t.Fatalf("SplitSurrogate=%X %X %v", hi, lo, ok)
	}
	r, ok := scalar.SurrogatePair(0xD801, 0xDC37)
	if !ok || r != 0x10437 {
		t.Fatalf("SurrogatePair=%U %v", r, ok)
	}
}

func TestU8SevenSamples(t *testing.T) {
	cases := []struct {
		in  []byte
		out []rune
	}{
		{[]byte{0xF0, 0x90, 0x80, 0x41}, []rune{0xFFFD, 0x41}},
		{[]byte{0xE0, 0x80, 0x80}, []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{[]byte{0xED, 0xA0, 0x80}, []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{[]byte{0xC0, 0xAF}, []rune{0xFFFD, 0xFFFD}},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, []rune{0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD}},
		{[]byte{0xE2, 0x82}, []rune{0xFFFD}}, // EOF 时
		{[]byte{0x80, 0x80}, []rune{0xFFFD, 0xFFFD}},
	}
	for _, c := range cases {
		var got []rune
		off := 0
		for off < len(c.in) {
			u := u8.DecodeAt(c.in[off:])
			if u.Size == 0 {
				got = append(got, 0xFFFD) // 模拟 EOF 冲刷
				break
			}
			got = append(got, u.Rune)
			off += u.Size
		}
		if len(got) != len(c.out) {
			t.Fatalf("% X => %U want %U", c.in, got, c.out)
		}
		for i := range got {
			if got[i] != c.out[i] {
				t.Fatalf("% X => %U want %U", c.in, got, c.out)
			}
		}
	}
}

func TestU8RoundTrip(t *testing.T) {
	var rs []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if scalar.IsScalar(r) {
			rs = append(rs, r)
		}
	}
	for _, r := range rs {
		b := u8.Encode(r)
		u := u8.DecodeAt(b)
		if u.Bad || u.Rune != r || u.Size != len(b) {
			t.Fatalf("roundtrip %U: %+v", r, u)
		}
	}
}

func TestU16Units(t *testing.T) {
	enc := func(ord u16.Order, rs ...rune) []byte {
		var b []byte
		for _, r := range rs {
			b = u16.EncodeAppend(b, ord, r)
		}
		return b
	}
	cases := []struct {
		name string
		in   []byte
		out  []rune
	}{
		{"pair", enc(u16.LE, 0x10437), []rune{0x10437}},
		{"loneHi", enc(u16.LE, 0xD800, 'A'), []rune{0xFFFD, 'A'}},
		{"loneLo", enc(u16.LE, 0xDC00), []rune{0xFFFD}},
		{"hiNonLo", enc(u16.LE, 0xD800, 0x41), []rune{0xFFFD, 0x41}},
	}
	for _, c := range cases {
		var got []rune
		var off int
		for off < len(c.in) {
			u := u16.DecodeAt(u16.LE, c.in[off:])
			if u.Size == 0 {
				break
			}
			got = append(got, u.Rune)
			if u.Keep {
				off += 2
			} else {
				off += u.Size
			}
		}
		if len(got) != len(c.out) {
			t.Fatalf("%s: %U want %U", c.name, got, c.out)
		}
		for i := range got {
			if got[i] != c.out[i] {
				t.Fatalf("%s: %U want %U", c.name, got, c.out)
			}
		}
	}
}
