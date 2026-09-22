package pct

import "testing"

func TestNormalizeUnreservedRestored(t *testing.T) {
	got, err := Normalize("%41%7e%2D")
	if err != nil {
		t.Fatal(err)
	}
	if got != "A~-" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeReservedKeptEscaped(t *testing.T) {
	got, err := Normalize("%2f%3A%25%20")
	if err != nil {
		t.Fatal(err)
	}
	if got != "%2F%3A%25%20" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeLiteralBytesPassThrough(t *testing.T) {
	got, err := Normalize("/a!$&'()*+,;=:@?")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/a!$&'()*+,;=:@?" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	in := "%41%2f%7Eabc%20%C3%A9"
	once, err := Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Normalize(once)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Fatalf("not idempotent: %q vs %q", once, twice)
	}
}

func TestErrorKindsAndOffsets(t *testing.T) {
	cases := []struct {
		in     string
		kind   ErrorKind
		offset int
	}{
		{"ab%", ErrTruncated, 2},
		{"ab%4", ErrTruncated, 2},
		{"ab%4x", ErrBadHex, 2},
		{"ab%x4", ErrBadHex, 2},
		{"ok%FF", ErrBadUTF8, 2},
		{"%C3%28", ErrBadUTF8, 0},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err == nil {
			t.Fatalf("%q: expected error, got %q", c.in, got)
		}
		pe, ok := err.(*Error)
		if !ok {
			t.Fatalf("%q: error type %T", c.in, err)
		}
		if pe.Kind != c.kind || pe.Offset != c.offset {
			t.Fatalf("%q: got kind=%d off=%d want kind=%d off=%d",
				c.in, pe.Kind, pe.Offset, c.kind, c.offset)
		}
		if got != "" {
			t.Fatalf("%q: partial result %q returned", c.in, got)
		}
	}
}

func TestRawInvalidUTF8Rejected(t *testing.T) {
	if _, err := Normalize("a\xffb"); err == nil {
		t.Fatal("expected error")
	} else if pe := err.(*Error); pe.Kind != ErrBadUTF8 || pe.Offset != 1 {
		t.Fatalf("got %v", err)
	}
}

func TestMultibyteEscapeSequenceValid(t *testing.T) {
	got, err := Normalize("%E4%B8%AD")
	if err != nil {
		t.Fatal(err)
	}
	if got != "%E4%B8%AD" {
		t.Fatalf("got %q", got)
	}
}
