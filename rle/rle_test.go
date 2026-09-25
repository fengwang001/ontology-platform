package rle

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/runs"
)

func errKey(err error) (error, int) {
	var de *DecodeError
	if errors.As(err, &de) {
		return de.Kind, de.Off
	}
	return nil, -1
}

func TestEncodeDecode(t *testing.T) {
	cases := []struct{ s, enc string }{
		{"", ""}, {"ab", "ab"}, {"aaab", "3ab"}, {"a1", `a\1`}, {"111", `3\1`},
		{`\\\`, `3\\`}, {"éé", "2é"}, {"éé", "éé"}, {"abb", "a2b"},
		{strings.Repeat("a", 12), "12a"}, {"a0b", `a\0b`}, {"🙂🙂", "2🙂"},
	}
	for _, c := range cases {
		if got := Encode(c.s); got != c.enc {
			t.Errorf("Encode(%q) = %q, want %q", c.s, got, c.enc)
		}
		if got, err := Decode(c.enc); err != nil || got != c.s {
			t.Errorf("Decode(%q) = %q, %v; want %q", c.enc, got, err, c.s)
		}
	}
}

func TestDecodeReject(t *testing.T) {
	cases := []struct {
		in   string
		kind error
		off  int
	}{
		{"1a", runs.ErrExplicitOne, 0}, {"a1b", runs.ErrExplicitOne, 1},
		{"0a", runs.ErrZeroCount, 0}, {"01a", runs.ErrLeadingZero, 0},
		{"2a3a", ErrAdjacentSame, 2}, {"aa", ErrAdjacentSame, 1},
		{`\a`, ErrBadEscape, 1}, {`\é`, ErrBadEscape, 1},
		{`a\`, ErrDanglingEscape, 1}, {`3\`, ErrDanglingEscape, 1},
		{"2", ErrMissingSymbol, 0}, {"a12", ErrMissingSymbol, 1},
		{"\xff", runs.ErrInvalidUTF8, 0}, {"a\xc3", runs.ErrInvalidUTF8, 1},
		{"\xed\xa0\x80", runs.ErrInvalidUTF8, 2},
		{"99999999999999999999a", ErrOutputLimit, 0},
	}
	for _, c := range cases {
		_, err := Decode(c.in)
		if kind, off := errKey(err); kind != c.kind || off != c.off {
			t.Errorf("Decode(%q) error = (%v, %d), want (%v, %d)", c.in, kind, off, c.kind, c.off)
		}
	}
}

func TestRoundtrip(t *testing.T) {
	fixed := []string{"", "a", "aaab", "0123456789", `\\\\`, "é漢字🙂", "éé", strings.Repeat("z", 1000)}
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("a19\\é漢\u0301🙂~")
	for i := 0; i < 300; i++ {
		var b strings.Builder
		for j := 0; j < rng.Intn(40); j++ {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		fixed = append(fixed, b.String())
	}
	for _, s := range fixed {
		back, err := Decode(Encode(s))
		if err != nil || back != s {
			t.Fatalf("roundtrip %q: %q, %v", s, back, err)
		}
		if enc := Encode(s); Encode(back) != enc {
			t.Fatalf("canonical form unstable for %q", s)
		}
	}
}

func TestBigCount(t *testing.T) {
	d := NewDecoder(20)
	d.Write([]byte("10a"))
	if s, err := d.Close(); err != nil || s != strings.Repeat("a", 10) {
		t.Fatalf("limit 20: %q, %v", s, err)
	}
	for _, limit := range []int64{0, 5, 9} {
		d := NewDecoder(limit)
		d.Write([]byte("10a"))
		if _, err := d.Close(); !errors.Is(err, ErrOutputLimit) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
	for _, in := range []string{"18446744073709551616a", "99999999999999999999a"} {
		if _, err := Decode(in); !errors.Is(err, ErrOutputLimit) {
			t.Errorf("Decode(%q): %v", in, err)
		}
	}
}

func TestSplitPoints(t *testing.T) {
	inputs := []string{"2a3a", "12aé\\1", `a\`, "1a", "\xff", "10é2b", "", "é2é", `3\\3\\`}
	for _, in := range inputs {
		want, werr := Decode(in)
		wk, wo := errKey(werr)
		for k := 0; k <= len(in); k++ {
			d := NewDecoder(1 << 30)
			d.Write([]byte(in[:k]))
			d.Write([]byte(in[k:]))
			got, err := d.Close()
			if gk, goff := errKey(err); got != want || gk != wk || goff != wo {
				t.Fatalf("split %q at %d: (%q,%v,%d), want (%q,%v,%d)", in, k, got, gk, goff, want, wk, wo)
			}
		}
		d := NewDecoder(1 << 30)
		for i := 0; i < len(in); i++ {
			d.Write([]byte(in[i : i+1]))
		}
		got, err := d.Close()
		if gk, goff := errKey(err); got != want || gk != wk || goff != wo {
			t.Fatalf("bytewise %q: (%q,%v,%d)", in, got, gk, goff)
		}
	}
}

func TestCheckedBytes(t *testing.T) {
	data := strings.Repeat(`3a3bé\1`, 1<<17) // exactly 1 MiB
	before := checkedByte
	d := NewDecoder(1 << 30)
	for i := 0; i < len(data); i++ {
		if _, err := d.Write([]byte(data[i : i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got := checkedByte - before; got != int64(len(data)) || len(data) != 1<<20 {
		t.Fatalf("checked %d bytes for %d input bytes", got, len(data))
	}
}
