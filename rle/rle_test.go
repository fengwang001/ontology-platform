package rle_test

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/rle"
)

func TestFormat(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""}, {"aaab", "3ab"}, {"a1", `a\1`}, {"111", `3\1`},
		{`\\\`, `3\\`}, {strings.Repeat("a", 12), "12a"}, {"ab", "ab"},
		{"é", "é"}, {"e\u0301", "e\u0301"}, // no Unicode normalization
	}
	for _, c := range cases {
		if got := rle.Encode(c.in); got != c.want {
			t.Errorf("Encode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	fixed := []string{"", "a", "0", `\`, "é", "e\u0301", strings.Repeat("中", 300), `a1\é中`}
	for _, s := range fixed {
		if got, err := rle.Decode(rle.Encode(s)); err != nil || got != s {
			t.Errorf("roundtrip %q: got %q, %v", s, got, err)
		}
	}
	rng := rand.New(rand.NewSource(1))
	alpha := []rune("a01\\é中\u0301")
	for i := 0; i < 500; i++ {
		var sb strings.Builder
		for n := rng.Intn(24); n >= 0; n-- {
			sb.WriteRune(alpha[rng.Intn(len(alpha))])
		}
		if got, err := rle.Decode(rle.Encode(sb.String())); err != nil || got != sb.String() {
			t.Fatalf("roundtrip %q: got %q, %v", sb.String(), got, err)
		}
	}
}

func TestReject(t *testing.T) {
	cases := []struct {
		in  string
		err error
		off int
	}{
		{"1a", rle.ErrExplicitOne, 0}, {`1\1`, rle.ErrExplicitOne, 0},
		{"0a", rle.ErrZeroCount, 0},
		{"01a", rle.ErrLeadingZero, 0}, {"00a", rle.ErrLeadingZero, 0},
		{"2a3a", rle.ErrAdjacentSame, 2}, {"aa", rle.ErrAdjacentSame, 1},
		{`\a`, rle.ErrBadEscape, 1}, {`a\é`, rle.ErrBadEscape, 2},
		{`\`, rle.ErrLoneEscape, 0}, {`a\`, rle.ErrLoneEscape, 1},
		{"12", rle.ErrNoSymbol, 0}, {"a3", rle.ErrNoSymbol, 1},
		{"\xff", rle.ErrBadUTF8, 0}, {"\xc3", rle.ErrBadUTF8, 0},
		{"a\xe4\xb8", rle.ErrBadUTF8, 1}, {"\xed\xa0\x80", rle.ErrBadUTF8, 1},
	}
	for _, c := range cases {
		_, err := rle.Decode(c.in)
		var de *rle.Error
		if !errors.As(err, &de) || de.Off != c.off || !errors.Is(err, c.err) {
			t.Errorf("Decode(%q) = %v, want %v at offset %d", c.in, err, c.err, c.off)
		}
	}
}

func TestBigCount(t *testing.T) {
	if _, err := rle.Decode("99999999999999999999a"); !errors.Is(err, rle.ErrTooLarge) {
		t.Errorf("huge count: %v", err)
	}
	d := rle.NewDecoder(3)
	if err := d.Write([]byte("5a")); !errors.Is(err, rle.ErrTooLarge) {
		t.Errorf("cap 3: %v", err)
	}
	d = rle.NewDecoder(5)
	if err := d.Write([]byte("5a")); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Close(); err != nil || got != "aaaaa" {
		t.Errorf("cap 5: %q, %v", got, err)
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	var ea, eb *rle.Error
	return errors.As(a, &ea) && errors.As(b, &eb) && ea.Off == eb.Off && ea.Err == eb.Err
}

func TestSplitPoints(t *testing.T) {
	for _, in := range []string{"2a3a", `12a\1b`, "é中3x", "10", `a\`, "1a", `3é\\`, "\xff"} {
		want, werr := rle.Decode(in)
		for cut := 0; cut <= len(in); cut++ {
			d := rle.NewDecoder(rle.DefaultMaxOutput)
			_ = d.Write([]byte(in[:cut]))
			_ = d.Write([]byte(in[cut:]))
			if got, err := d.Close(); got != want || !sameErr(err, werr) {
				t.Errorf("%q cut %d: %q, %v", in, cut, got, err)
			}
		}
		d := rle.NewDecoder(rle.DefaultMaxOutput)
		for i := range len(in) {
			_ = d.Write([]byte(in[i : i+1]))
		}
		if got, err := d.Close(); got != want || !sameErr(err, werr) {
			t.Errorf("%q bytewise: %q, %v", in, got, err)
		}
	}
}

func TestCounter(t *testing.T) {
	enc := strings.Repeat("ab", 1<<19) // 1 MiB of canonical text
	before := rle.BytesExamined()
	d := rle.NewDecoder(rle.DefaultMaxOutput)
	for i := range len(enc) {
		if err := d.Write([]byte(enc[i : i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if n := rle.BytesExamined() - before; n != int64(len(enc)) {
		t.Errorf("examined %d bytes, want %d", n, len(enc))
	}
}
