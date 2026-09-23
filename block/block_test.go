package block

import (
	"errors"
	"testing"
)

func TestEncodeParseAt(t *testing.T) {
	cases := []struct {
		name string
		ss   []string
		k    int
	}{
		{"empty", nil, 4},
		{"single", []string{"a"}, 4},
		{"empty-first", []string{"", "a", "ab"}, 2},
		{"same-prefix", []string{"aaaa", "aaab", "aaac", "aaad"}, 2},
		{"no-prefix", []string{"a", "b", "c", "d"}, 2},
		{"k-one", []string{"a", "ab", "abc"}, 1},
		{"k-bigger-than-n", []string{"x", "y"}, 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Encode(tc.ss, tc.k)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			b, err := Parse(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if b.Len() != len(tc.ss) {
				t.Fatalf("len = %d want %d", b.Len(), len(tc.ss))
			}
			for i, want := range tc.ss {
				if got := b.At(i); got != want {
					t.Fatalf("at %d = %q want %q", i, got, want)
				}
			}
		})
	}
}

func TestOrderRejected(t *testing.T) {
	cases := []struct {
		name string
		ss   []string
		pos  int
	}{
		{"duplicate", []string{"a", "a"}, 1},
		{"prefix-backstep", []string{"ab", "a"}, 1},
		{"decrease", []string{"b", "a"}, 1},
		{"late", []string{"a", "b", "d", "c"}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Encode(tc.ss, 4)
			var oe *OrderError
			if !errors.As(err, &oe) || oe.Pos != tc.pos {
				t.Fatalf("err = %v want OrderError pos %d", err, tc.pos)
			}
		})
	}
}

func TestBytePrefixUTF8(t *testing.T) {
	// shared length is in bytes: cafe(5B)/cafes(6B) share 5;
	// rune-wise sharing would be 4.
	raw, err := Encode([]string{"caf\u00e9", "caf\u00e9s"}, 16)
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseHeader(raw)
	if err != nil {
		t.Fatal(err)
	}
	data := raw[hdrLen : hdrLen+h.dataLen]
	// entry0: shared=0 diff=5 ; entry1: shared=5 diff=1
	got0, got1 := u32(data[0:4])&^restartFlag, u32(data[8+5:8+5+4])
	if got0 != 0 || got1 != 5 {
		t.Fatalf("shared = %d,%d want 0,5 (bytes); rune-wise would be 4", got0, got1)
	}
	b, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if b.At(1) != "caf\u00e9s" {
		t.Fatalf("at1 = %q", b.At(1))
	}
}
