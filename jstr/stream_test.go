package jstr_test

import (
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"ontology/jstr"
)

func TestRoundTrip(t *testing.T) {
	fixed := []string{"", "a", `"\`, "\x00\x1f\x7f", "é世界", "\U0001F600",
		"\u2028\u2029", string(rune(0x10FFFF)), strings.Repeat("z\"\\\n", 50)}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		var sb strings.Builder
		for n := rng.Intn(10); n >= 0; n-- {
			if r := rune(rng.Intn(0x110000)); utf8.ValidRune(r) {
				sb.WriteRune(r)
			}
		}
		fixed = append(fixed, sb.String())
	}
	for _, s := range fixed {
		lit, err := jstr.Encode(s)
		if err != nil {
			t.Fatalf("Encode(%q): %v", s, err)
		}
		var std string
		if err := json.Unmarshal(lit, &std); err != nil || std != s {
			t.Fatalf("stdlib rejects Encode(%q) = %s: %v", s, lit, err)
		}
		if back, err := jstr.Decode(lit); err != nil || back != s {
			t.Fatalf("Decode(Encode(%q)) = %q, %v", s, back, err)
		}
		stdLit, _ := json.Marshal(s)
		if back, err := jstr.Decode(stdLit); err != nil || back != s {
			t.Fatalf("Decode(json.Marshal(%q)) = %q, %v", s, back, err)
		}
	}
}

func TestMinimalFixedPoint(t *testing.T) {
	ys := []string{
		`""`, `"abc"`, `"\""`, `"\\"`, `"/"`, `"\b\f\n\r\t"`,
		`"\u0000\u001f\u001b"`, "\"\x7f\"", "\"\u2028\u2029\"", "\"é世界😀\"",
	}
	for _, y := range ys {
		d, err := jstr.Decode([]byte(y))
		if err != nil {
			t.Fatalf("Decode(%s): %v", y, err)
		}
		if got, _ := jstr.Encode(d); string(got) != y {
			t.Errorf("Encode(Decode(%s)) = %s", y, got)
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	var ea, eb *jstr.Error
	if !errors.As(a, &ea) || !errors.As(b, &eb) {
		return false
	}
	return errors.Is(a, eb.Err) && ea.Off == eb.Off
}

func decodeChunks(lit []byte, cuts ...int) (string, error) {
	d := new(jstr.Decoder)
	prev := 0
	for _, c := range cuts {
		if err := d.Write(lit[prev:c]); err != nil {
			return "", err
		}
		prev = c
	}
	if err := d.Write(lit[prev:]); err != nil {
		return "", err
	}
	return d.Close()
}

func TestSplitConsistency(t *testing.T) {
	lits := []string{
		`"hello"`, `"a\uD83D\uDE00z\n"`, "\"\xc3\xa9\U0001F600\"",
		`"\u0041\u00e9"`, `"\uD83D"`, `"\uDE00"`, `"\uD83DA"`,
		`"\u12G4"`, "\"a\x01\"", `"\x"`, `"ab`, `"a"x`,
	}
	for _, lit := range lits {
		want, wantErr := jstr.Decode([]byte(lit))
		for cut := 0; cut <= len(lit); cut++ {
			got, err := decodeChunks([]byte(lit), cut)
			if got != want || !sameErr(err, wantErr) {
				t.Errorf("split %q at %d: got %q, %v; want %q, %v",
					lit, cut, got, err, want, wantErr)
			}
		}
		if got, err := decodeChunks([]byte(lit), cuts1(len(lit))...); got != want || !sameErr(err, wantErr) {
			t.Errorf("1-byte feed of %q: got %q, %v; want %q, %v", lit, got, err, want, wantErr)
		}
	}
}

func TestByteCheckBudget(t *testing.T) {
	lit := []byte{'"'}
	for len(lit) < 1<<20 {
		lit = append(lit, `\uD83D\uDE00`...)
	}
	lit = append(lit, '"')
	before := jstr.Checked()
	if _, err := decodeChunks(lit, cuts1(len(lit))...); err != nil {
		t.Fatal(err)
	}
	if got := jstr.Checked() - before; got > 2*int64(len(lit)) {
		t.Fatalf("checked %d bytes for %d input bytes (budget 2x)", got, len(lit))
	}
}

func cuts1(n int) []int {
	var c []int
	for i := 1; i < n; i++ {
		c = append(c, i)
	}
	return c
}
