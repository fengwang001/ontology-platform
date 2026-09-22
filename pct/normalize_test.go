package pct

import (
	"errors"
	"testing"
)

func TestNormalizeUnreservedAndHexCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"%41", "A"},
		{"%7e", "~"},
		{"%2f", "%2F"},
		{"%2F", "%2F"},
		{"a%42c", "aBc"},
		{"%c3%a9", "%C3%A9"},
		{"/", "/"},
	}
	for _, c := range cases {
		got, _, err := Normalize(c.in)
		if err != nil || got != c.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
		got2, _, err := Normalize(got)
		if err != nil || got2 != got {
			t.Errorf("not idempotent for %q: %q, %v", c.in, got2, err)
		}
	}
}

func TestNormalizeChanged(t *testing.T) {
	if _, changed, _ := Normalize("abc"); changed {
		t.Error("plain string reported changed")
	}
	if _, changed, _ := Normalize("%41"); !changed {
		t.Error("%41 -> A should report changed")
	}
	if _, changed, _ := Normalize("%2f"); !changed {
		t.Error("lowercase hex should report changed")
	}
}

func TestMalformedEscapes(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
		off  int
		sent error
	}{
		{"abc%", KindShort, 3, ErrShortEscape},
		{"a%4", KindShort, 1, ErrShortEscape},
		{"%GG", KindBadHex, 1, ErrBadHex},
		{"%4G", KindBadHex, 2, ErrBadHex},
		{"%E2%82", KindUTF8, 0, ErrInvalidUTF8},
		{"ab%FF", KindUTF8, 2, ErrInvalidUTF8},
		{"%C0%80", KindUTF8, 0, ErrInvalidUTF8},
	}
	for _, c := range cases {
		out, _, err := Normalize(c.in)
		if err == nil {
			t.Fatalf("Normalize(%q) expected error, got %q", c.in, out)
		}
		if out != "" {
			t.Fatalf("Normalize(%q) returned partial result %q", c.in, out)
		}
		var ee *EscapeError
		if !errors.As(err, &ee) {
			t.Fatalf("error for %q is not *EscapeError: %T", c.in, err)
		}
		if ee.Kind != c.kind || ee.Offset != c.off {
			t.Errorf("Normalize(%q): kind=%d off=%d; want kind=%d off=%d",
				c.in, ee.Kind, ee.Offset, c.kind, c.off)
		}
		if !errors.Is(err, c.sent) {
			t.Errorf("Normalize(%q): errors.Is mismatch for %v", c.in, c.sent)
		}
	}
}

func TestValidUTF8Decoded(t *testing.T) {
	// "é" encoded as %C3%A9 stays escaped but must validate as UTF-8.
	if _, _, err := Normalize("%C3%A9"); err != nil {
		t.Fatalf("valid UTF-8 rejected: %v", err)
	}
	// Decoded unreserved sequences remain ASCII anyway.
}
