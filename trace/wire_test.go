package trace

import (
	"errors"
	"testing"
)

// Semantics 5: wire round-trip preserves trace ID, span ID, parent ID,
// sampling flag and all baggage; Marshal is byte-deterministic.
func TestWireRoundTrip(t *testing.T) {
	root := NewRoot(seqGen(), true)
	root.SetBaggage("user", "alice")
	root.SetBaggage("tenant", "acme")
	root.SetBaggage("ab", "first")
	wire1 := root.Marshal()
	wire2 := root.Marshal()
	if wire1 != wire2 {
		t.Fatalf("Marshal not deterministic:\n%s\n%s", wire1, wire2)
	}
	got, err := Parse(wire1)
	if err != nil {
		t.Fatalf("Parse(%q): %v", wire1, err)
	}
	if got.TraceID() != root.TraceID() {
		t.Errorf("TraceID = %q, want %q", got.TraceID(), root.TraceID())
	}
	if got.SpanID() != root.SpanID() {
		t.Errorf("SpanID = %q, want %q", got.SpanID(), root.SpanID())
	}
	if got.ParentID() != root.ParentID() {
		t.Errorf("ParentID = %q, want %q", got.ParentID(), root.ParentID())
	}
	if got.Sampled() != root.Sampled() {
		t.Errorf("Sampled = %v, want %v", got.Sampled(), root.Sampled())
	}
	for _, k := range root.BaggageKeys() {
		want, _ := root.Baggage(k)
		if v, ok := got.Baggage(k); !ok || v != want {
			t.Errorf("baggage %q = %q,%v want %q,true", k, v, ok, want)
		}
	}
	if len(got.BaggageKeys()) != len(root.BaggageKeys()) {
		t.Errorf("parsed has %d keys, want %d", len(got.BaggageKeys()), len(root.BaggageKeys()))
	}
	if got.Marshal() != wire1 {
		t.Errorf("re-marshal = %q, want %q", got.Marshal(), wire1)
	}
}

// Semantics 5 (cont.): the exact wire layout, including ascending
// baggage order and the sampling flag.
func TestMarshalLayout(t *testing.T) {
	n := 0
	gen := func() SpanID { n++; return SpanID(string(rune('0' + n))) }
	s := NewRoot(gen, true)
	s.SetBaggage("b", "2")
	s.SetBaggage("a", "1")
	if got, want := s.Marshal(), "1-1-01-a=1-b=2"; got != want {
		t.Fatalf("Marshal = %q, want %q", got, want)
	}
	unsampled := NewRoot(gen, false)
	if got, want := unsampled.Marshal(), "2-2-00"; got != want {
		t.Fatalf("Marshal = %q, want %q", got, want)
	}
}

// Semantics 6: malformed inputs yield (nil, ErrMalformed); a valid
// string without baggage parses fine.
func TestParseMalformed(t *testing.T) {
	bad := []string{
		"",
		"onlytrace",
		"trace-span",
		"-span-01",
		"trace--01",
		"trace-span-1",
		"trace-span-10",
		"trace-span-yes",
		"trace-span-01-kv",
		"trace-span-00-=v",
	}
	for _, in := range bad {
		s, err := Parse(in)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q) err = %v, want ErrMalformed", in, err)
		}
		if s != nil {
			t.Errorf("Parse(%q) returned non-nil span on error", in)
		}
	}
	for _, in := range []string{"trace-span-01", "trace-span-00"} {
		s, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): unexpected %v", in, err)
			continue
		}
		if len(s.BaggageKeys()) != 0 {
			t.Errorf("Parse(%q): baggage = %v, want empty", in, s.BaggageKeys())
		}
	}
}

// Semantics 8 (parse side): the parsed span and the original share no
// mutable state — mutating either leaves the other untouched.
func TestParseDoesNotShareState(t *testing.T) {
	orig := NewRoot(seqGen(), true)
	orig.SetBaggage("k", "orig")
	parsed, err := Parse(orig.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	parsed.SetBaggage("k", "parsed-rewrote")
	parsed.SetBaggage("parsed-only", "x")
	if v, _ := orig.Baggage("k"); v != "orig" {
		t.Fatalf("orig: k = %q, want orig (parsed write leaked)", v)
	}
	if _, ok := orig.Baggage("parsed-only"); ok {
		t.Fatal("orig saw key added to parsed span")
	}
	orig.SetBaggage("k", "orig-rewrote")
	orig.SetBaggage("orig-only", "y")
	if v, _ := parsed.Baggage("k"); v != "parsed-rewrote" {
		t.Fatalf("parsed: k = %q, want parsed-rewrote (orig write leaked)", v)
	}
	if _, ok := parsed.Baggage("orig-only"); ok {
		t.Fatal("parsed saw key added to orig")
	}
}
