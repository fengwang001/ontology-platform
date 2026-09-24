package rle

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestEncodeFormat(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""}, {"aaab", "3ab"}, {"a1", `a\1`}, {"111", `3\1`},
		{`\\\`, `3\\`}, {"aaaaaaaaaaaa", "12a"}, {"ab", "ab"},
		{"éé", "2é"}, {"e\u0301e\u0301", "e\u0301e\u0301"},
	}
	for _, c := range cases {
		if got := Encode(c.in); got != c.want {
			t.Errorf("Encode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	fixed := []string{"", "a", "a1", `\\1`, "é日\u0301", strings.Repeat("z", 5000), "1234567890"}
	for _, s := range fixed {
		if got, err := Decode(Encode(s)); err != nil || got != s {
			t.Fatalf("roundtrip %q: got %q, err %v", s, got, err)
		}
	}
	alpha := []rune{'a', '1', '9', '\\', 'é', '\u0301', '日'}
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 300; iter++ {
		var b strings.Builder
		for k, n := 0, 1+rng.IntN(400); k < n; k++ {
			b.WriteRune(alpha[rng.IntN(len(alpha))])
		}
		if rng.IntN(2) == 0 {
			b.WriteString(strings.Repeat(string(alpha[rng.IntN(len(alpha))]), rng.IntN(300)))
		}
		s := b.String()
		if got, err := Decode(Encode(s)); err != nil || got != s {
			t.Fatalf("random roundtrip %q: got %q, err %v", s, got, err)
		}
	}
}

func TestDecodeReject(t *testing.T) {
	cases := []struct {
		in   string
		kind error
		off  int
	}{
		{"1a", ErrCountOne, 0}, {"0a", ErrCountZero, 0}, {"0", ErrCountZero, 0},
		{"01a", ErrLeadingZero, 0}, {"007b", ErrLeadingZero, 0},
		{"2a3a", ErrAdjacentSame, 2}, {"aa", ErrAdjacentSame, 1},
		{`\a`, ErrBadEscape, 0}, {`\é`, ErrBadEscape, 0},
		{"a\\", ErrTrailingEscape, 1}, {"3", ErrMissingSymbol, 1},
		{"ab12", ErrMissingSymbol, 4}, {"\xff", ErrInvalidUTF8, 0},
		{"a\xc3", ErrInvalidUTF8, 1},
	}
	for _, c := range cases {
		_, err := Decode(c.in)
		var de *Error
		if !errors.As(err, &de) || !errors.Is(err, c.kind) || de.Off != c.off {
			t.Errorf("Decode(%q) = %v, want %v at %d", c.in, err, c.kind, c.off)
		}
	}
}

func TestBigCount(t *testing.T) {
	if _, err := Decode("99999999999999999999a"); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("huge count: got %v, want ErrOutputLimit", err)
	}
	if _, err := DecodeLimit("2a", 1); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("tiny limit: got %v, want ErrOutputLimit", err)
	}
	got, err := DecodeLimit("1000000b", 1<<20)
	if err != nil || got != strings.Repeat("b", 1000000) || Encode(got) != "1000000b" {
		t.Fatalf("large-in-limit: %q..., err %v", got[:5], err)
	}
}

func stream(in string, sizes []int) (string, error) {
	var buf bytes.Buffer
	d := NewDecoder(&buf, DefaultLimit)
	i := 0
	for _, n := range sizes {
		if i+n > len(in) {
			n = len(in) - i
		} else if n > 0 {
			d.Write([]byte(in[i : i+n]))
		}
		i += n
	}
	if i < len(in) {
		d.Write([]byte(in[i:]))
	}
	return buf.String(), d.Close()
}

func byteWise(in string) (string, error) {
	var buf bytes.Buffer
	d := NewDecoder(&buf, DefaultLimit)
	for i := 0; i < len(in); i++ {
		d.Write([]byte{in[i]})
	}
	return buf.String(), d.Close()
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func TestSplitPoints(t *testing.T) {
	inputs := []string{"2a3a", `3\1x`, "12é3\\", "abc", "1a", "0b", `\1\1`, "a\xc3"}
	rng := rand.New(rand.NewPCG(3, 4))
	for _, in := range inputs {
		wantS, wantE := stream(in, nil)
		for cut := 0; cut <= len(in); cut++ {
			if s, e := stream(in, []int{cut}); s != wantS || errStr(e) != errStr(wantE) {
				t.Fatalf("cut %d of %q: (%q,%v) want (%q,%v)", cut, in, s, e, wantS, wantE)
			}
		}
		if s, e := byteWise(in); s != wantS || errStr(e) != errStr(wantE) {
			t.Fatalf("bytewise %q: (%q,%v)", in, s, e)
		}
		var sizes []int
		for n := 0; n < len(in); {
			size := 1 + rng.IntN(7)
			sizes, n = append(sizes, size), n+size
		}
		if s, e := stream(in, sizes); s != wantS || errStr(e) != errStr(wantE) {
			t.Fatalf("random chunking of %q: %v", in, e)
		}
	}
}

func TestCheckedBytes(t *testing.T) {
	enc := Encode(strings.Repeat("ab1\\é", 150000))
	if len(enc) < 1<<20 {
		t.Fatalf("test input only %d bytes", len(enc))
	}
	before := CheckedBytes()
	d := NewDecoder(io.Discard, DefaultLimit)
	for i := 0; i < len(enc); i++ {
		d.Write([]byte(enc[i : i+1]))
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got := CheckedBytes() - before; got != int64(len(enc)) {
		t.Fatalf("checked = %d, want %d", got, len(enc))
	}
}
