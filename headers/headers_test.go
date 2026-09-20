package headers

import (
	"errors"
	"reflect"
	"testing"
	"unsafe"
)

func TestLineFormat(t *testing.T) {
	s, err := Parse("Host: example.com\r\nX-Keep: a  b\tc \r\n", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := s.Get("Host"); !ok || v != "example.com" {
		t.Fatalf("Host = %q, %v", v, ok)
	}
	if v, ok := s.Get("X-Keep"); !ok || v != "a  b\tc" {
		t.Fatalf("X-Keep = %q, %v (inner whitespace must be preserved)", v, ok)
	}

	bad := []string{
		"Host : example.com\r\n",
		"Host\texample.com\r\n",
		"Host: bad\n",
		"Host: bad",
		": value\r\n",
		"Host: bad\rline\r\n",
	}
	for _, raw := range bad {
		if s, err := Parse(raw, nil); !errors.Is(err, ErrMalformed) || s != nil {
			t.Fatalf("Parse(%q) = %v, %v; want nil, ErrMalformed", raw, s, err)
		}
	}
}

func TestCanonicalNames(t *testing.T) {
	s, err := Parse("content-type: text/plain\r\nX-REQUEST-ID: 42\r\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"Content-Type", "X-Request-Id"}
	if got := s.Names(); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("Names = %v; want %v", got, wantNames)
	}
	if v, ok := s.Get("CONTENT-TYPE"); !ok || v != "text/plain" {
		t.Fatalf("case-insensitive lookup failed: %q, %v", v, ok)
	}
	if got := s.Values("Content-TYPE"); len(got) != 1 || got[0] != "42" && got[0] != "text/plain" {
		t.Fatalf("Values case-insensitive lookup failed: %v", got)
	}
}

func TestObsFold(t *testing.T) {
	s, err := Parse("X-Folded: first   \r\n second\r\n\tthird\r\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Get("X-Folded"); !ok || v != "first second third" {
		t.Fatalf("folded value = %q, %v", v, ok)
	}

	s2, err := Parse("X-Lead:   \r\n\ttail\r\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s2.Get("X-Lead"); !ok || v != "tail" {
		t.Fatalf("fold with empty first line = %q, %v", v, ok)
	}

	if s, err := Parse(" continuation\r\nHost: x\r\n", nil); !errors.Is(err, ErrMalformed) || s != nil {
		t.Fatalf("leading continuation = %v, %v", s, err)
	}
}

func TestMultiValues(t *testing.T) {
	raw := "Set-Cookie: a=1\r\nSet-Cookie: b=2, x=3\r\n"
	s, err := Parse(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a=1", "b=2, x=3"}
	if got := s.Values("set-cookie"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Values = %v; want %v", got, want)
	}
	if v, ok := s.Get("Set-Cookie"); !ok || v != "a=1, b=2, x=3" {
		t.Fatalf("Get = %q, %v", v, ok)
	}

	got := s.Values("missing")
	if got != nil {
		t.Fatalf("Values for missing header = %v; want nil", got)
	}

	vals := s.Values("Set-Cookie")
	vals[0] = "mutated"
	if s.Values("Set-Cookie")[0] != "a=1" {
		t.Fatal("Values returned a slice backed by internal storage")
	}
}

func TestSingleValue(t *testing.T) {
	s, err := Parse("Content-Length: 5\r\ncontent-length: 5\r\n", []string{"content-length"})
	if err != nil || s == nil {
		t.Fatalf("identical duplicate = %v, %v", s, err)
	}
	if got := s.Values("Content-Length"); len(got) != 1 || got[0] != "5" {
		t.Fatalf("identical duplicate not deduped: %v", got)
	}

	if s, err := Parse("Content-Length: 5\r\nContent-Length: 6\r\n", []string{"Content-Length"}); !errors.Is(err, ErrSingleValue) || s != nil {
		t.Fatalf("conflicting single-value = %v, %v", s, err)
	}
}

func TestEmptyValue(t *testing.T) {
	s, err := Parse("X-Empty:\r\nX-Space:   \r\nHost: x\r\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Get("X-Empty"); !ok || v != "" {
		t.Fatalf("empty value = %q, %v", v, ok)
	}
	if v, ok := s.Get("X-Space"); !ok || v != "" {
		t.Fatalf("whitespace-only value = %q, %v", v, ok)
	}
	if _, ok := s.Get("Absent"); ok {
		t.Fatal("absent header reported as present")
	}
	if got := s.Values("X-Empty"); len(got) != 1 || got[0] != "" {
		t.Fatalf("empty value missing from Values: %v", got)
	}
}

func TestNoAliasAndRepeatable(t *testing.T) {
	buf := []byte("X-A: v1\r\nX-B: v2\r\n")
	s1, err := Parse(unsafe.String(&buf[0], len(buf)), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range buf {
		buf[i] = 'Z'
	}
	if got := s1.Names(); !reflect.DeepEqual(got, []string{"X-A", "X-B"}) {
		t.Fatalf("Parse depended on the input backing array: %v", got)
	}

	s2, _ := Parse("X-A: v1\r\nX-B: v2\r\n", nil)
	if !reflect.DeepEqual(s1.Names(), s2.Names()) {
		t.Fatal("repeated Parse produced different Names")
	}
	if !reflect.DeepEqual(s1.Values("X-A"), s2.Values("X-A")) {
		t.Fatal("repeated Parse produced different Values")
	}
	g1, _ := s1.Get("X-B")
	g2, _ := s2.Get("X-B")
	if g1 != g2 {
		t.Fatal("repeated Parse produced different Get results")
	}
}

func TestEmptyInput(t *testing.T) {
	s, err := Parse("", nil)
	if err != nil || s == nil {
		t.Fatalf("empty input = %v, %v", s, err)
	}
	if len(s.Names()) != 0 {
		t.Fatalf("empty input produced names: %v", s.Names())
	}
}
