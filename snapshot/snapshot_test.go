package snapshot

import (
	"maps"
	"testing"
)

func TestFreeze(t *testing.T) {
	cases := []struct {
		name        string
		typ, id     string
		props       map[string]string
		wantKey     string
		wantVal     string
		wantPresent bool
	}{
		{"empty", "task", "t1", nil, "x", "", false},
		{"one prop", "task", "t1", map[string]string{"a": "1"}, "a", "1", true},
		{"many props", "task", "t1", map[string]string{"b": "2", "a": "1", "c": "3"}, "b", "2", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Freeze(tc.typ, tc.id, tc.props)
			if s.Type() != tc.typ || s.ID() != tc.id {
				t.Fatalf("header = %q/%q, want %q/%q", s.Type(), s.ID(), tc.typ, tc.id)
			}
			if v, ok := s.Get(tc.wantKey); ok != tc.wantPresent || v != tc.wantVal {
				t.Fatalf("Get(%q) = %q,%v want %q,%v", tc.wantKey, v, ok, tc.wantVal, tc.wantPresent)
			}
		})
	}
}

func TestDeterminismAndIsolation(t *testing.T) {
	// Properties inserted in different orders must serialize identically.
	orders := []map[string]string{
		{"start": "1", "end": "9", "owner": "ann"},
		{"owner": "ann", "end": "9", "start": "1"},
		{"end": "9", "start": "1", "owner": "ann"},
	}
	first := Freeze("task", "t1", orders[0])
	for i, m := range orders[1:] {
		got := Freeze("task", "t1", m)
		if !got.Equal(first) {
			t.Fatalf("order %d: bytes differ for identical content", i+1)
		}
	}

	// Mutating the source map and the returned byte slice must not change the snapshot.
	src := map[string]string{"a": "1"}
	s := Freeze("task", "t1", src)
	src["a"] = "999"
	src["b"] = "2"
	raw := s.Bytes()
	for i := range raw {
		raw[i] = 0xFF
	}
	if v, _ := s.Get("a"); v != "1" {
		t.Fatalf("snapshot mutated via source map: a = %q", v)
	}
	if _, ok := s.Get("b"); ok {
		t.Fatal("snapshot leaked newly inserted key b")
	}
	if s.Equal(Freeze("task", "t1", map[string]string{"a": "999", "b": "2"})) {
		t.Fatal("snapshot bytes changed after external mutation")
	}

	// Different content must differ.
	if s.Equal(Freeze("task", "t1", map[string]string{"a": "2"})) {
		t.Fatal("distinct content compared equal")
	}
	if Freeze("task", "t1", src).Equal(Freeze("job", "t1", src)) {
		t.Fatal("distinct types compared equal")
	}

	// Internal map copy is independent of the source.
	if maps.Equal(s.props, src) {
		t.Fatal("internal props alias the caller map")
	}
}
