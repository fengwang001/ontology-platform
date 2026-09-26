package esc

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func TestEscapeVectors(t *testing.T) {
	cases := []struct {
		in, want []byte
	}{
		{nil, []byte{}},
		{[]byte("hi"), []byte("hi")},
		{[]byte("a\nb"), []byte(`a\nb`)},
		{[]byte(`x\y`), []byte(`x\\y`)},
		{[]byte("\\\n"), []byte(`\\\n`)},
	}
	for _, c := range cases {
		if got := Escape(c.in); !bytes.Equal(got, c.want) {
			t.Errorf("Escape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		p := make([]byte, rng.Intn(64))
		for j := range p {
			switch rng.Intn(4) {
			case 0:
				p[j] = EscapeByte
			case 1:
				p[j] = Newline
			default:
				p[j] = byte(rng.Intn(256))
			}
		}
		got, err := Unescape(Escape(p))
		if err != nil || !bytes.Equal(got, p) {
			t.Fatalf("round trip failed for %q: got %q, err %v", p, got, err)
		}
	}
}

func TestUnescapeErrors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"invalid escape x", []byte(`\x`), ErrInvalidEscape},
		{"invalid escape 0", []byte(`a\0b`), ErrInvalidEscape},
		{"dangling at end", []byte(`ab\`), ErrDanglingEscape},
		{"lone backslash", []byte(`\`), ErrDanglingEscape},
	}
	for _, c := range cases {
		got, err := Unescape(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if got != nil {
			t.Errorf("%s: got %q, want nil", c.name, got)
		}
	}
	if ErrInvalidEscape == ErrDanglingEscape {
		t.Fatal("sentinel errors must be distinct")
	}
}
