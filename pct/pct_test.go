package pct

import (
	"strings"
	"testing"
)

func TestNormalizeBasic(t *testing.T) {
	cases := map[string]string{
		"%41":              "A",
		"%4a":              "J",
		"a%2Fb":            "a%2Fb",
		"%2f":              "%2F",
		"%7E":              "~",
		"plain":            "plain",
		"%E4%B8%AD":        "%E4%B8%AD", // decoded UTF-8 for 中, re-escaped
		"%e4%b8%ad":        "%E4%B8%AD",
		"\xe4\xb8\xad":     "\xe4\xb8\xad",
		"a%2F%2Fb":         "a%2F%2Fb",
		"%41%42%43":        "ABC",
		"%C3%A9":           "%C3%A9", // é
	}
	for in, want := range cases {
		got, _, _, err := Normalize(in)
		if err != nil {
			t.Fatalf("Normalize(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChangedFlag(t *testing.T) {
	if _, changed, _, err := Normalize("abc"); err != nil || changed {
		t.Fatalf("abc: changed=%v err=%v", changed, err)
	}
	if _, changed, _, err := Normalize("%41"); err != nil || !changed {
		t.Fatalf("%%41: changed=%v err=%v", changed, err)
	}
	if _, changed, _, err := Normalize("%2f"); err != nil || !changed {
		t.Fatalf("%%2f uppercase: changed=%v err=%v", changed, err)
	}
	if _, changed, _, err := Normalize("%2F"); err != nil || changed {
		t.Fatalf("%%2F canonical: changed=%v err=%v", changed, err)
	}
}

func TestErrors(t *testing.T) {
	check := func(in string, k Kind, off int) {
		t.Helper()
		_, _, _, err := Normalize(in)
		e, ok := AsError(err)
		if !ok {
			t.Fatalf("%q: want *Error, got %v", in, err)
		}
		if e.Kind != k || e.Offset != off {
			t.Fatalf("%q: got kind=%d off=%d, want kind=%d off=%d", in, e.Kind, e.Offset, k, off)
		}
	}
	check("abc%", KindShortEscape, 3)
	check("abc%4", KindShortEscape, 3)
	check("ab%GG", KindBadHex, 2)
	check("ab%2g", KindBadHex, 2)
	check("%E4%B8%", KindShortEscape, 6)
	check("\xff", KindInvalidUTF8, 0)
	check("%FF", KindInvalidUTF8, 0)
	check("%C0%80", KindInvalidUTF8, 0) // overlong NUL
	check("%ED%A0%80", KindInvalidUTF8, 0) // UTF-16 surrogate U+D800
	check("a%E4%B8", KindInvalidUTF8, 1)  // truncated multibyte rune
	check("%F4%90%80%80", KindInvalidUTF8, 0) // > U+10FFFF
	check("%E4%B8%AD%FF", KindInvalidUTF8, 9)
}

func TestScannedEqualsLength(t *testing.T) {
	for n := 0; n < 300; n++ {
		s := strings.Repeat("a", n)
		if _, _, scanned, err := Normalize(s); err != nil || scanned != n {
			t.Fatalf("n=%d scanned=%d err=%v", n, scanned, err)
		}
	}
}

func TestIdempotent(t *testing.T) {
	inputs := []string{"%41%2F", "%e4%b8%ad", "a%2fb%7E", "%41%42"}
	for _, in := range inputs {
		first, _, _, err := Normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		second, changed, _, err := Normalize(first)
		if err != nil {
			t.Fatal(err)
		}
		if first != second || changed {
			t.Fatalf("not idempotent: %q -> %q -> %q changed=%v", in, first, second, changed)
		}
	}
}
