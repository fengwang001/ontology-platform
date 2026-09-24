package rle

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/runs"
)

func TestEncode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""}, {"aaab", "3ab"}, {"a1", `a\1`}, {"111", `3\1`},
		{`\\\`, `3\\`}, {"aaaaaaaaaaaa", "12a"}, {"ab", "ab"}, {"é", "é"},
		{"2", `\2`}, {`\`, `\\`}, {"aéé", "a2é"}, {"e\u0301", "e\u0301"},
	}
	for _, c := range cases {
		if got := Encode(c.in); got != c.want {
			t.Errorf("Encode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	fixed := []string{"", "a", "aaab", "a1", "111", `\\\`, "é", "e\u0301",
		"ééé000\\a中", strings.Repeat("x", 100000)}
	for _, s := range fixed {
		if got, err := Decode(Encode(s)); err != nil || got != s {
			t.Errorf("round trip %q: got %q, err %v", s, got, err)
		}
	}
	rng := rand.New(rand.NewSource(1))
	alpha := []rune(`ab019\é中` + "\u0301")
	for i := 0; i < 500; i++ {
		var b []rune
		for n := rng.Intn(40); n >= 0; n-- {
			r := alpha[rng.Intn(len(alpha))]
			for k := rng.Intn(4) + 1; k > 0; k-- {
				b = append(b, r)
			}
		}
		s := string(b)
		if got, err := Decode(Encode(s)); err != nil || got != s {
			t.Fatalf("round trip %q: got %q, err %v", s, got, err)
		}
	}
}

func TestReject(t *testing.T) {
	cases := []struct {
		in   string
		kind error
		off  int64
	}{
		{"1a", runs.ErrExplicitOne, 0}, {`1\1`, runs.ErrExplicitOne, 0},
		{"0a", runs.ErrZeroCount, 0}, {"01a", runs.ErrLeadingZero, 0},
		{"00a", runs.ErrLeadingZero, 0}, {"2a3a", ErrAdjacentSame, 2},
		{"aa", ErrAdjacentSame, 1}, {"a2a", ErrAdjacentSame, 1},
		{`\1\1`, ErrAdjacentSame, 2}, {`\q`, ErrBadEscape, 1},
		{`2\q`, ErrBadEscape, 2}, {`\`, ErrLoneBackslash, 0},
		{`2\`, ErrLoneBackslash, 1}, {"2", ErrMissingSymbol, 0},
		{"a12", ErrMissingSymbol, 1}, {"\xff", ErrInvalidUTF8, 0},
		{"a\xc3", ErrInvalidUTF8, 1}, {"\xed\xa0\x80", ErrInvalidUTF8, 0},
		{"\xc0\xaf", ErrInvalidUTF8, 0}, {"\xf4\x90\x80\x80", ErrInvalidUTF8, 0},
	}
	for _, c := range cases {
		_, err := Decode(c.in)
		var de *Error
		if !errors.As(err, &de) || !errors.Is(err, c.kind) || de.Off != c.off {
			t.Errorf("Decode(%q) err = %v, want kind %v at %d", c.in, err, c.kind, c.off)
		}
	}
}

func TestCanonical(t *testing.T) {
	cases := []string{"", "a", "3ab", `a\1`, `3\\`, "12a", "é", "2é中", `2\2`}
	for _, s := range cases {
		got, err := Decode(s)
		if err != nil {
			t.Fatalf("Decode(%q): %v", s, err)
		}
		if re := Encode(got); re != s {
			t.Errorf("Encode(Decode(%q)) = %q", s, re)
		}
	}
}

func TestBigCount(t *testing.T) {
	if _, err := Decode("99999999999999999999a"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("huge count: %v", err)
	}
	d := NewDecoder(3)
	if _, err := d.Write([]byte("4a")); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over limit: %v", err)
	}
	d = NewDecoder(3)
	d.Write([]byte("3a"))
	if s, err := d.Close(); err != nil || s != "aaa" {
		t.Errorf("at limit: %q, %v", s, err)
	}
	big := strings.Repeat("z", 1_000_000)
	if got, err := Decode(Encode(big)); err != nil || got != big {
		t.Error("big run round trip failed")
	}
}

func TestCombining(t *testing.T) {
	pre, de := "é", "e\u0301"
	if Encode(de) != de || Encode(pre) != pre {
		t.Error("encoder must not normalize")
	}
	for _, s := range []string{pre, de, de + de, pre + de} {
		if got, err := Decode(Encode(s)); err != nil || got != s {
			t.Errorf("round trip %q: %q, %v", s, got, err)
		}
	}
}
