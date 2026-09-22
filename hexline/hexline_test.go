package hexline

import (
	"errors"
	"testing"
)

func TestParseSizes(t *testing.T) {
	cases := map[string]uint64{
		"0":                0,
		"a":                10,
		"A":                10,
		"fF":               255,
		"004":              4,
		"1A2b":             6699,
		"ffffffffffffffff": 1<<64 - 1,
	}
	for in, want := range cases {
		got, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseExtensionsIgnored(t *testing.T) {
	cases := []string{
		"4;foo=bar",
		"4;foo",
		"4;a=1;b=2;c",
		`4;a="x;y=z"`,    // ';' and '=' inside quotes are not delimiters
		`4;a="q\"w";b=2`, // escaped quote does not end the string
		`4;a="\";";b=9`,  // quoted semicolon right after escaped quote
		"4;a=;b=",        // empty values
		`4;a="\\"`,       // escaped backslash then closing quote
		"10;x=y",         // size 16 with extension
	}
	for _, in := range cases {
		want := uint64(4)
		if in == "10;x=y" {
			want = 16
		}
		got, err := Parse([]byte(in))
		if err != nil {
			t.Errorf("Parse(%q): unexpected %v", in, err)
		} else if got != want {
			t.Errorf("Parse(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrNotHex},
		{"Z", ErrNotHex},
		{"g1", ErrNotHex},
		{"1Z", ErrNotHex},
		{" 1", ErrNotHex},
		{";a=b", ErrNotHex},
		{"4 a", ErrNotHex},
		{"10000000000000000", ErrSizeOverflow}, // 2^64
		{"fffffffffffffffff", ErrSizeOverflow}, // 17 f's
		{`4;a="unterminated`, ErrUnterminatedQuote},
		{`4;a="trailing\`, ErrUnterminatedQuote},
		{`4;a="`, ErrUnterminatedQuote},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.in))
		if !errors.Is(err, c.want) {
			t.Errorf("Parse(%q) err = %v, want %v", c.in, err, c.want)
		}
	}
}

func TestParseErrorsDistinct(t *testing.T) {
	sentinels := []error{ErrNotHex, ErrSizeOverflow, ErrUnterminatedQuote,
		ErrLineTooLong, ErrBareLF, ErrBareCR}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinels %v and %v are not distinct", a, b)
			}
		}
	}
}

func TestScannerFragments(t *testing.T) {
	full := "4;a=1\r\nrest"
	for split := 0; split <= len(full); split++ {
		s := NewScanner(64)
		line, n1, done, err := s.Write([]byte(full[:split]))
		if err != nil {
			t.Fatalf("split %d: first Write: %v", split, err)
		}
		n2 := 0
		if !done {
			line, n2, done, err = s.Write([]byte(full[split:]))
			if err != nil {
				t.Fatalf("split %d: %v", split, err)
			}
		}
		if !done || string(line) != "4;a=1" {
			t.Fatalf("split %d: line=%q done=%v", split, line, done)
		}
		if n1+n2 != len("4;a=1\r\n") {
			t.Fatalf("split %d: consumed %d+%d", split, n1, n2)
		}
	}
}

func TestScannerLimitsAndEndings(t *testing.T) {
	s := NewScanner(3)
	if _, n, _, err := s.Write([]byte("abcd")); !errors.Is(err, ErrLineTooLong) || n != 4 {
		t.Errorf("too long: n=%d err=%v", n, err)
	}
	s = NewScanner(3)
	if _, _, _, err := s.Write([]byte("ab\ncd")); !errors.Is(err, ErrBareLF) {
		t.Errorf("bare LF: %v", err)
	}
	s = NewScanner(3)
	if _, _, _, err := s.Write([]byte("ab\rx")); !errors.Is(err, ErrBareCR) {
		t.Errorf("bare CR: %v", err)
	}
	s = NewScanner(3)
	if _, _, _, err := s.Write([]byte("ab\r")); err != nil || !s.PendingCR() {
		t.Errorf("pending CR: err=%v pending=%v", err, s.PendingCR())
	}
}
