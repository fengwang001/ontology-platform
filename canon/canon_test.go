package canon

import (
	"errors"
	"testing"

	"ontology/pct"
)

func norm(t *testing.T, in string) string {
	t.Helper()
	got, err := New(Config{}).Normalize(in)
	if err != nil {
		t.Fatalf("%q: %v", in, err)
	}
	return got
}

func TestSyntaxNormalization(t *testing.T) {
	cases := map[string]string{
		"HTTP://ExAmPLE.COM./a":      "http://example.com/a",
		"http://example.com:80/a":    "http://example.com/a",
		"https://example.com:443/a":  "https://example.com/a",
		"http://example.com:8080/a":  "http://example.com:8080/a",
		"http://example.com:080/a":   "http://example.com/a",
		"http://example.com:/a":      "http://example.com/a",
		"http://example.com":         "http://example.com/",
		"http://example.com?x=1":     "http://example.com/?x=1",
		"http://example.com/a#frag":  "http://example.com/a",
		"http://example.com/%41%7e":  "http://example.com/A~",
		"http://example.com/a%2fb":   "http://example.com/a%2Fb",
		"ftp://example.com:80/a":     "ftp://example.com:80/a",
		"http://example.com/a/../..": "http://example.com/",
	}
	for in, want := range cases {
		if got := norm(t, in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestEscapedSlashNotEquivalent(t *testing.T) {
	n := New(Config{})
	eq, err := n.Equivalent("http://h/a%2Fb", "http://h/a/b")
	if err != nil {
		t.Fatal(err)
	}
	if eq {
		t.Fatal("/a%2Fb and /a/b must not be equivalent")
	}
}

func TestDotAndTrailingSlashSemantics(t *testing.T) {
	if got := norm(t, "http://h/../../x"); got != "http://h/x" {
		t.Fatalf("got %q", got)
	}
	n := New(Config{})
	eq, _ := n.Equivalent("http://h/a", "http://h/a/")
	if eq {
		t.Fatal("/a and /a/ must not be equivalent")
	}
}

func TestQueryModesAndThreeStates(t *testing.T) {
	ord := New(Config{Mode: ModeOrdered})
	srt := New(Config{Mode: ModeSorted})
	eq, _ := ord.Equivalent("http://h/?a=1&a=2", "http://h/?a=2&a=1")
	if eq {
		t.Fatal("ordered mode must distinguish a=1&a=2 from a=2&a=1")
	}
	eq, _ = srt.Equivalent("http://h/?a=1&a=2", "http://h/?a=2&a=1")
	if !eq {
		t.Fatal("sorted mode must equate a=1&a=2 and a=2&a=1")
	}
	for _, pair := range [][2]string{
		{"http://h/?a", "http://h/?a="},
		{"http://h/?a=", "http://h/?a=%20"},
		{"http://h/?a", "http://h/?a=%20"},
	} {
		eq, _ := srt.Equivalent(pair[0], pair[1])
		if eq {
			t.Fatalf("%q and %q must differ", pair[0], pair[1])
		}
	}
}

func TestInvalidEscapesDistinguishable(t *testing.T) {
	n := New(Config{})
	kinds := map[pct.ErrorKind]bool{}
	for _, in := range []string{
		"http://h/a%4",
		"http://h/a%4x",
		"http://h/a%FF",
		"http://h/?q=%",
	} {
		got, err := n.Normalize(in)
		if err == nil {
			t.Fatalf("%q: expected error, got %q", in, got)
		}
		if got != "" {
			t.Fatalf("%q: partial result %q", in, got)
		}
		var pe *pct.Error
		if !errors.As(err, &pe) {
			t.Fatalf("%q: error type %T", in, err)
		}
		kinds[pe.Kind] = true
	}
	if len(kinds) != 3 {
		t.Fatalf("expected 3 distinguishable kinds, got %v", kinds)
	}
}

func TestIPv6AndPortNormalization(t *testing.T) {
	n := New(Config{})
	eq, err := n.Equivalent(
		"http://[2001:0db8:0000:0000:0000:0000:0000:0001]/",
		"http://[2001:db8::1]/")
	if err != nil || !eq {
		t.Fatalf("IPv6 zero compression: eq=%v err=%v", eq, err)
	}
	if got := norm(t, "http://[::1]:8080/"); got != "http://[::1]:8080/" {
		t.Fatalf("got %q", got)
	}
	eq, _ = n.Equivalent("http://[2001:db8::1]:0080/a", "http://[2001:db8::1]/a")
	if !eq {
		t.Fatal("IPv6 with default port must drop the port")
	}
}
