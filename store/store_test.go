package store_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"ontology/store"
)

// op is one step of a reference replay.
type op struct {
	k, v string
	del  bool
}

// replay is the naive merge on an initially empty base, replaying all history.
func replay(ops []op) map[string]string {
	m := map[string]string{}
	for _, o := range ops {
		if o.del {
			delete(m, o.k)
		} else {
			m[o.k] = o.v
		}
	}
	return m
}

func TestNaiveRecompute(t *testing.T) {
	cases := []struct {
		T   int
		ops []op
	}{
		{4, []op{{"a", "1", false}, {"b", "2", false}, {"c", "3", false},
			{"c", "", true}, {"b", "9", false}, {"c", "7", false}, {"c", "8", false}}},
		{2, []op{{"x", "0", false}, {"x", "1", false}, {"x", "", true},
			{"y", "2", false}, {"x", "3", false}, {"y", "", true}, {"x", "4", false}}},
	}
	for _, tc := range cases {
		s, err := store.New(tc.T)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		want := replay(tc.ops)
		for _, o := range tc.ops {
			if o.del {
				_ = s.Del(o.k)
			} else {
				_ = s.Set(o.k, o.v)
			}
		}
		for k, w := range want {
			got, ok := s.Read(k)
			if !ok || got != w {
				t.Fatalf("T=%d key %q = (%q,%v), want %q", tc.T, k, got, ok, w)
			}
		}
		if _, ok := s.Read("missing-key"); ok {
			t.Fatalf("T=%d absent key read as present", tc.T)
		}
	}
}

func TestTombstone(t *testing.T) {
	s, _ := store.New(4)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Four entries force a compaction; c's tombstone must be removed, not blanked.
	must(s.Set("c", "3"))
	must(s.Set("d", "4"))
	must(s.Set("e", "5"))
	must(s.Del("c"))
	if v, ok := s.Read("c"); ok {
		t.Fatalf("deleted c read as present: (%q,%v); blank-fold would give (\"\",true)", v, ok)
	}
	if slices.Contains(s.BaseKeys(), "c") {
		t.Fatal("tombstone left c (possibly empty-valued) in base")
	}
	// Delete then rewrite returns the latest value.
	must(s.Set("c", "7"))
	must(s.Set("c", "8"))
	if v, ok := s.Read("c"); !ok || v != "8" {
		t.Fatalf("rewritten c = (%q,%v), want (8,true)", v, ok)
	}
	// Deleting an absent key must not create presence.
	must(s.Del("never"))
	if _, ok := s.Read("never"); ok {
		t.Fatal("Del on an absent key created presence")
	}
}

func TestMergeCostBounded(t *testing.T) {
	// The cost counter is private; at the instant after Read it equals DeltaLen.
	cases := []struct {
		T, m int
	}{{4, 100}, {4, 1000}, {4, 10000}, {8, 1003}, {64, 9999}}
	for _, tc := range cases {
		s, err := store.New(tc.T)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < tc.m; i++ {
			_ = s.Set(fmt.Sprintf("k%d", i), "x")
		}
		if _, ok := s.Read("k0"); !ok {
			t.Fatalf("m=%d: k0 missing", tc.m)
		}
		if c := s.DeltaLen(); c >= tc.T {
			t.Fatalf("T=%d m=%d merge cost %d grows with history (want < T)", tc.T, tc.m, c)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, bad := range []int{0, -1, -100} {
		s, err := store.New(bad)
		if !errors.Is(err, store.ErrBadThreshold) || s != nil {
			t.Fatalf("New(%d) = %v, %v", bad, s, err)
		}
	}
	if store.ErrEmptyKey == store.ErrBadThreshold {
		t.Fatal("the two sentinel errors must be distinct")
	}
	s, _ := store.New(4)
	_ = s.Set("a", "1")
	before, dl := s.BaseKeys(), s.DeltaLen()
	v, ok := s.Read("a")
	if err := s.Set("", "x"); !errors.Is(err, store.ErrEmptyKey) {
		t.Fatalf("Set empty key: %v", err)
	}
	if err := s.Del(""); !errors.Is(err, store.ErrEmptyKey) {
		t.Fatalf("Del empty key: %v", err)
	}
	if !reflect.DeepEqual(s.BaseKeys(), before) || s.DeltaLen() != dl {
		t.Fatal("a rejected op mutated base/delta")
	}
	if g, o := s.Read("a"); g != v || o != ok {
		t.Fatal("a rejected op changed a later read")
	}
	_ = s.Set("a", "9") // store stays usable after rejection
	if g, _ := s.Read("a"); g != "9" {
		t.Fatalf("post-rejection Set gave %q, want 9", g)
	}
}
