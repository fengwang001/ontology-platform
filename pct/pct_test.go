package pct

import "testing"

func TestNormalizeUnreservedFold(t *testing.T) {
	cases := map[string]string{
		"%41":       "A",
		"%61%62":    "ab",
		"%2e":       ".",
		"%7E":       "~",
		"a%2Fb":     "a%2Fb", // reserved: must stay encoded
		"%3a":       "%3A",   // reserved, hex uppercased
		"%2f":       "%2F",
		"plain":     "plain",
		"%E4%B8%AD": "%E4%B8%AD", // valid UTF-8, reserved bytes stay
		"%25":       "%25",       // '%' itself is reserved
		"a b":       "a b",
	}
	for in, want := range cases {
		got, err := Normalize(in)
		if err != nil {
			t.Fatalf("Normalize(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	inputs := []string{"%41%2f%2E%7e", "a%3Ab%25c", "%E4%B8%AD%2F"}
	for _, in := range inputs {
		once, err := Normalize(in)
		if err != nil {
			t.Fatalf("first pass: %v", err)
		}
		twice, err := Normalize(once)
		if err != nil {
			t.Fatalf("second pass: %v", err)
		}
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}

func TestErrorsAreDistinguishable(t *testing.T) {
	cases := []struct {
		in      string
		kind    Kind
		wantOff int
	}{
		{"abc%", KindTruncated, 3},
		{"abc%2", KindTruncated, 3},
		{"ab%zz", KindBadHex, 3},
		{"ab%2z", KindBadHex, 4},
		{"%ff", KindBadUTF8, 0},
		{"ok%E4%B8", KindBadUTF8, 2}, // truncated UTF-8 seq via escapes
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err == nil {
			t.Fatalf("Normalize(%q) succeeded with %q, want error", c.in, got)
		}
		pe, ok := err.(*Error)
		if !ok {
			t.Fatalf("Normalize(%q) error type %T, want *pct.Error", c.in, err)
		}
		if pe.Kind != c.kind {
			t.Errorf("Normalize(%q) kind = %v, want %v", c.in, pe.Kind, c.kind)
		}
		if pe.Offset != c.wantOff {
			t.Errorf("Normalize(%q) offset = %d, want %d", c.in, pe.Offset, c.wantOff)
		}
		if got != "" {
			t.Errorf("Normalize(%q) returned partial result %q", c.in, got)
		}
	}
}

func TestDecode(t *testing.T) {
	b, err := Decode("a%20b%2Fc")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "a b/c" {
		t.Errorf("Decode = %q", b)
	}
	if _, err := Decode("%"); err == nil {
		t.Error("Decode of lone % should fail")
	}
}
