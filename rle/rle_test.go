package rle

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func mustDecode(t *testing.T, s string) string {
	t.Helper()
	got, err := Decode(s)
	if err != nil {
		t.Fatalf("Decode(%q): %v", s, err)
	}
	return got
}

func decodeBytes(in string) (string, error) {
	d := &Decoder{}
	for _, b := range []byte(in) {
		if _, err := d.Write([]byte{b}); err != nil {
			return "", err
		}
	}
	return d.Close()
}

func sameErr(a, b error) bool {
	var ea, eb *Error
	if !errors.As(a, &ea) || !errors.As(b, &eb) {
		return a == nil && b == nil
	}
	return ea.Offset == eb.Offset && errors.Is(a, eb.Err)
}

func TestEncode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"aaab", "3ab"},
		{"a1", `a\1`},
		{"111", `3\1`},
		{`\\\`, `3\\`},
		{strings.Repeat("a", 12), "12a"},
		{"aabb", "2a2b"},
		{"éé", "2é"},
		{"e\u0301e\u0301", "e\u0301e\u0301"}, // 组合序列：不合并、不规范化
	}
	for _, c := range cases {
		if got := Encode(c.in); got != c.want {
			t.Errorf("Encode(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := mustDecode(t, c.want); got != c.in {
			t.Errorf("Decode(%q) = %q, want %q", c.want, got, c.in)
		}
		if re := Encode(mustDecode(t, c.want)); re != c.want {
			t.Errorf("Encode(Decode(%q)) = %q, 非规范形式", c.want, re)
		}
	}
}

func TestReject(t *testing.T) {
	cases := []struct {
		in   string
		sent error
		off  int
	}{
		{"1a", ErrCountOne, 0},
		{`1\1`, ErrCountOne, 0},
		{"0a", ErrCountZero, 0},
		{"01a", ErrLeadingZero, 0},
		{"00a", ErrLeadingZero, 0},
		{"2a3a", ErrAdjacentSame, 3},
		{"aa", ErrAdjacentSame, 1},
		{`2\12\1`, ErrAdjacentSame, 4},
		{`\a`, ErrBadEscape, 1},
		{`\é`, ErrBadEscape, 1},
		{`\`, ErrLoneEscape, 0},
		{"a\\", ErrLoneEscape, 1},
		{"3", ErrNoSymbol, 0},
		{"2a3", ErrNoSymbol, 2},
		{"\xff", ErrBadUTF8, 0},
		{"a\xc3", ErrBadUTF8, 1},
		{"\xc3(", ErrBadUTF8, 0},
		{"99999999999999999999a", ErrTooLong, 20},
	}
	for _, c := range cases {
		_, err := Decode(c.in)
		var de *Error
		if !errors.Is(err, c.sent) || !errors.As(err, &de) || de.Offset != c.off {
			t.Errorf("Decode(%q) = %v, want %v @%d", c.in, err, c.sent, c.off)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	alphabet := []rune{'a', 'a', 'b', '9', '0', '\\', 'é', '€', '\u0301', '中'}
	for i := 0; i < 3000; i++ {
		var sb strings.Builder
		for n := r.Intn(30); n > 0; n-- {
			sb.WriteRune(alphabet[r.Intn(len(alphabet))])
		}
		s, enc := sb.String(), ""
		enc = Encode(s)
		dec, err := Decode(enc)
		if err != nil || dec != s {
			t.Fatalf("往返失败 %q -> %q: dec=%q err=%v", s, enc, dec, err)
		}
		if Encode(dec) != enc {
			t.Fatalf("非规范编码 %q", enc)
		}
	}
}
