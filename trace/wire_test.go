package trace_test

import (
	"errors"
	"testing"

	"ontology/trace"
)

func assertSameSpan(t *testing.T, want, got *trace.Span) {
	t.Helper()
	if want.TraceID() != got.TraceID() ||
		want.SpanID() != got.SpanID() ||
		want.ParentID() != got.ParentID() ||
		want.Sampled() != got.Sampled() {
		t.Fatalf("round trip mismatch: want %s got %s", want.Marshal(), got.Marshal())
	}
	for _, k := range want.BaggageKeys() {
		wv, _ := want.Baggage(k)
		gv, ok := got.Baggage(k)
		if !ok || gv != wv {
			t.Fatalf("baggage %q: want %q got %q,%v", k, wv, gv, ok)
		}
	}
	if len(want.BaggageKeys()) != len(got.BaggageKeys()) {
		t.Fatalf("baggage size: want %d got %d",
			len(want.BaggageKeys()), len(got.BaggageKeys()))
	}
}

func TestWireRoundTrip(t *testing.T) {
	root := trace.NewRoot(seqGen(), true)
	root.SetBaggage("user", "alice")
	root.SetBaggage("region", "cn-north")

	child := root.Child()
	child.SetBaggage("user", "bob")
	child.SetBaggage("op", "charge")

	grandchild := child.Child()

	for _, s := range []*trace.Span{root, child, grandchild} {
		got, err := trace.Parse(s.Marshal())
		if err != nil {
			t.Fatalf("Parse(%q): %v", s.Marshal(), err)
		}
		assertSameSpan(t, s, got)
	}
}

func TestMarshalDeterministic(t *testing.T) {
	s := trace.NewRoot(seqGen(), true)
	for _, k := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		s.SetBaggage(k, "v-"+k)
	}
	first := s.Marshal()
	for i := 0; i < 100; i++ {
		if got := s.Marshal(); got != first {
			t.Fatalf("call %d: %q != %q", i, got, first)
		}
	}
}

func TestParseMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"too few fields":    "t-s-01",
		"too many fields":   "t-s-p-01-x",
		"bad flag":          "t-s-p-1",
		"flag not 01/00":    "t-s-p-10",
		"empty trace id":    "-s-p-01",
		"empty span id":     "t--p-01",
		"baggage no sep":    "t-s-p-01;justkey",
		"baggage empty key": "t-s-p-01;=v",
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			span, err := trace.Parse(wire)
			if !errors.Is(err, trace.ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}
			if span != nil {
				t.Fatalf("span = %v, want nil", span)
			}
		})
	}
}

func TestParseValidNoBaggage(t *testing.T) {
	for _, wire := range []string{"t-s-p-01", "t-s-p-00", "t-s--01"} {
		s, err := trace.Parse(wire)
		if err != nil {
			t.Fatalf("Parse(%q): %v", wire, err)
		}
		if len(s.BaggageKeys()) != 0 {
			t.Fatalf("Parse(%q): unexpected baggage %v", wire, s.BaggageKeys())
		}
	}
}

func TestNoSharedMutableState(t *testing.T) {
	root := trace.NewRoot(seqGen(), true)
	root.SetBaggage("k", "root")

	parsed, err := trace.Parse(root.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	parsed.SetBaggage("k", "parsed")
	parsed.SetBaggage("extra", "x")
	if v, _ := root.Baggage("k"); v != "root" {
		t.Fatalf("mutating parsed span changed original: %q", v)
	}
	if _, ok := root.Baggage("extra"); ok {
		t.Fatal("parsed span's new key leaked into original")
	}

	child := root.Child()
	child.SetBaggage("k", "child")
	if v, _ := root.Baggage("k"); v != "root" {
		t.Fatalf("mutating child changed parent: %q", v)
	}
	root.SetBaggage("k", "root-again")
	if v, _ := child.Baggage("k"); v != "child" {
		t.Fatalf("mutating parent changed child: %q", v)
	}
}
