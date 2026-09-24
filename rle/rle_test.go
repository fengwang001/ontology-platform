package rle

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestEncode(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"aaab":         "3ab",
		"a1":           "a\\1",
		"111":          "3\\1",
		"\\\\\\":       "3\\\\",
		"aaaaaaaaaaaa": "12a",
		"aab":          "2ab",
		"é":            "é",
		"e\u0301":      "e\u0301",
	}
	for in, want := range cases {
		if got := Encode(in); got != want {
			t.Fatalf("Encode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []string{
		"", "a", "aaab", "a1", "111", "\\\\\\", "aaaaaaaaaaaa",
		"0123456789", "mixed 9 and \\ and 世界", "éé", "e\u0301e\u0301",
		strings.Repeat("z", 5000), "a\u0000b\ufffd世",
	}
	for _, s := range cases {
		out, err := Decode(Encode(s))
		if err != nil || out != s {
			t.Fatalf("roundtrip %q: got %q err=%v", s, out, err)
		}
	}
}

func TestRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune{'a', 'b', '1', '0', '9', '\\', 'é', '世', '\u0301', '\n', '\x00'}
	for iter := 0; iter < 200; iter++ {
		var sb strings.Builder
		n := rng.IntN(64)
		for i := 0; i < n; i++ {
			sb.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		s := sb.String()
		enc := Encode(s)
		out, err := Decode(enc)
		if err != nil || out != s {
			t.Fatalf("iter %d roundtrip %q: got %q err=%v", iter, s, out, err)
		}
		if re, err := Decode(enc); err != nil || Encode(re) != enc {
			t.Fatalf("iter %d not canonical: %q", iter, enc)
		}
	}
}

func TestRejectCases(t *testing.T) {
	cases := []struct {
		in   string
		want error
		off  int
	}{
		{"1a", ErrCountOne, 1},
		{"2a1b", ErrCountOne, 3},
		{"0a", ErrCountZero, 0},
		{"b0a", ErrCountZero, 1},
		{"01a", ErrLeadingZero, 0},
		{"00a", ErrLeadingZero, 0},
		{"2a3a", ErrAdjacentSymbol, 3},
		{"aa", ErrAdjacentSymbol, 1},
		{"2\\1\\1", ErrAdjacentSymbol, 4},
		{"\\x", ErrBadEscape, 0},
		{"a\\n", ErrBadEscape, 1},
		{"\\", ErrTrailingSlash, 0},
		{"2", ErrTrailingCount, 0},
		{"a2", ErrTrailingCount, 1},
		{"\xff", ErrInvalidUTF8, 0},
		{"\xc0\x80", ErrInvalidUTF8, 0},
		{"a\x80", ErrInvalidUTF8, 1},
	}
	for _, c := range cases {
		_, err := Decode(c.in)
		var de *DecodeError
		if !errors.As(err, &de) || !errors.Is(err, c.want) || de.Offset != c.off {
			t.Fatalf("Decode(%q) err=%v off=%d, want %v @%d", c.in, err, offset(err), c.want, c.off)
		}
	}
}

func offset(err error) int {
	var de *DecodeError
	if errors.As(err, &de) {
		return de.Offset
	}
	return -1
}

func TestHugeCount(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"99999999999999999999a", ErrCountTooLarge},
		{"9223372036854775808a", ErrCountTooLarge},
		{"9223372036854775807a", ErrOutputLimit},
	}
	for _, c := range cases {
		if _, err := Decode(c.in); !errors.Is(err, c.want) {
			t.Fatalf("Decode(%q) err=%v, want %v", c.in, err, c.want)
		}
	}
}

func TestOutputLimit(t *testing.T) {
	d := NewDecoder(discardW{}, 4)
	if _, err := d.Write([]byte("10a")); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("err=%v, want ErrOutputLimit", err)
	}
}

type discardW struct{}

func (discardW) Write(p []byte) (int, error) { return len(p), nil }

func TestStreamingSplits(t *testing.T) {
	inputs := []string{
		"", "a", "12a3b2\\1", "2a3a", "1a", "01a", "\\\\", "\\1",
		"世2界", "a\\", "2", "0", "\xc0\x80", "3é", "éé",
		"99999999999999999999a",
	}
	for _, in := range inputs {
		want, wErr := Decode(in)
		wOff := offset(wErr)
		for k := 0; k <= len(in); k++ {
			var sb bytes.Buffer
			d := NewDecoder(&sb, DefaultOutputLimit)
			var err error
			if k > 0 {
				_, err = d.Write([]byte(in[:k]))
			}
			if err == nil && k < len(in) {
				_, err = d.Write([]byte(in[k:]))
			}
			if err == nil {
				err = d.Close()
			}
			if (err == nil) != (wErr == nil) || err == nil && sb.String() != want {
				t.Fatalf("split %q@%d: out=%q err=%v, want %q err=%v", in, k, sb.String(), err, want, wErr)
			}
			if err != nil && (!errors.Is(err, wErr) || offset(err) != wOff) {
				t.Fatalf("split %q@%d: err=%v@%d, want @%d", in, k, err, offset(err), wOff)
			}
		}
	}
}

func TestCheckedCounter(t *testing.T) {
	var sb bytes.Buffer
	d := NewDecoder(&sb, DefaultOutputLimit)
	text := strings.Repeat("x", 1<<20)
	enc := []byte(Encode(text))
	for i := 0; i < len(enc); i++ {
		if _, err := d.Write(enc[i : i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if d.Checked() != len(enc) {
		t.Fatalf("checked=%d, want %d", d.Checked(), len(enc))
	}
	if sb.Len() != len(text) {
		t.Fatalf("out len=%d, want %d", sb.Len(), len(text))
	}
}
