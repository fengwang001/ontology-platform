package dim

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func E(k string, v int64) Entry { return Entry{Key: k, Val: v} }

// TestEightStepSequence replays the NOTES.md eight-step derivation and pins
// V, used and retained versions at every step.
func TestEightStepSequence(t *testing.T) {
	s := New(100)
	type want struct {
		batch []Entry // nil => lookup step
		key   string  // lookup fields
		vsn   int64
		val   int64
		kin   LookupKind
		v     int64
		used  int64
		vers  []int64
	}
	steps := []want{
		{batch: []Entry{E("a", 1), E("b", 2)}, v: 1, used: 18, vers: []int64{0, 1}},
		{batch: []Entry{E("c", 3), E("d", 4)}, v: 2, used: 54, vers: []int64{0, 1, 2}},
		{key: "a", vsn: 1, val: 1, kin: Found, v: 2, used: 54, vers: []int64{0, 1, 2}},
		{batch: []Entry{E("a", 9)}, v: 3, used: 90, vers: []int64{0, 1, 2, 3}},
		{key: "a", vsn: 1, val: 1, kin: Found, v: 3, used: 90, vers: []int64{0, 1, 2, 3}},
		{key: "a", vsn: 3, val: 9, kin: Found, v: 3, used: 90, vers: []int64{0, 1, 2, 3}},
		{batch: []Entry{E("e", 5), E("f", 6)}, v: 4, used: 90, vers: []int64{3, 4}},
		{key: "a", vsn: 1, kin: Stale, v: 4, used: 90, vers: []int64{3, 4}},
	}
	for i, st := range steps {
		if st.batch != nil {
			if v, err := s.Broadcast(st.batch); err != nil || v != st.v {
				t.Fatalf("step %d: v=%d err=%v want %d", i+1, v, err, st.v)
			}
		} else if val, kin, err := s.Lookup(st.vsn, st.key); !errors.Is(err, nil) ||
			kin != st.kin || val != st.val {
			t.Fatalf("step %d: val=%d kin=%d err=%v want val=%d kin=%d",
				i+1, val, kin, err, st.val, st.kin)
		}
		if s.V() != st.v || s.Used() != st.used ||
			!reflect.DeepEqual(s.Versions(), st.vers) || s.Oldest() != st.vers[0] {
			t.Fatalf("step %d: V=%d used=%d vers=%v oldest=%d", i+1, s.V(), s.Used(), s.Versions(), s.Oldest())
		}
	}
}

// TestLookupClass covers found / miss / stale / future. Upsert means a Key
// present before is still present after (here v2 keeps a and adds b).
func TestLookupClass(t *testing.T) {
	s := New(100)
	s.Broadcast([]Entry{E("a", 7)})            // v1 = {a}
	s.Broadcast([]Entry{E("a", 7), E("b", 8)}) // v2 = {a,b}
	cases := []struct {
		vsn int64
		key string
		val int64
		kin LookupKind
		err error
	}{
		{1, "a", 7, Found, nil},
		{2, "a", 7, Found, nil},
		{2, "b", 8, Found, nil},
		{2, "x", 0, Miss, nil},
		{0, "a", 0, Miss, nil}, // empty v0 retained: miss, not stale
		{-1, "a", 0, Stale, nil},
		{3, "a", 0, 0, ErrFuture},
	}
	for i, c := range cases {
		val, kin, err := s.Lookup(c.vsn, c.key)
		if !errors.Is(err, c.err) || kin != c.kin || val != c.val {
			t.Fatalf("case %d: val=%d kin=%d err=%v want val=%d kin=%d err=%v",
				i, val, kin, err, c.val, c.kin, c.err)
		}
	}
}

// TestRejectedBroadcast verifies every rejection path leaves V/used/snapshots
// untouched and the store stays usable afterwards.
func TestRejectedBroadcast(t *testing.T) {
	cases := []struct {
		name string
		capB int64
		seed [][]Entry
		bad  []Entry
		err  error
		v    int64
		used int64
		vers []int64
	}{
		{"empty batch", 100, [][]Entry{{E("a", 1)}}, nil, ErrEmptyBatch, 1, 9, []int64{0, 1}},
		{"empty key", 100, nil, []Entry{{Key: "", Val: 1}}, ErrEmptyKey, 0, 0, []int64{0}},
		{"V and V-1 still over cap", 20, [][]Entry{{E("a", 1)}},
			[]Entry{E("b", 2), E("c", 3), E("d", 4)}, ErrTooBig, 1, 9, []int64{0, 1}},
		{"first batch alone over cap", 10, nil, []Entry{E("a", 1), E("b", 2)}, ErrTooBig, 0, 0, []int64{0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(c.capB)
			for _, b := range c.seed {
				if _, err := s.Broadcast(b); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Broadcast(c.bad); !errors.Is(err, c.err) {
				t.Fatalf("err=%v want %v", err, c.err)
			}
			if s.V() != c.v || s.Used() != c.used || !reflect.DeepEqual(s.Versions(), c.vers) {
				t.Fatalf("after reject: V=%d used=%d vers=%v want %d %d %v",
					s.V(), s.Used(), s.Versions(), c.v, c.used, c.vers)
			}
			// An overwrite of the existing key keeps the new snapshot at 9 B,
			// fitting alongside V-1 under the tight cap, proving reuse.
			if v, err := s.Broadcast([]Entry{E("a", 2)}); err != nil || v != c.v+1 {
				t.Fatalf("unusable after rejection: v=%d err=%v", v, err)
			}
		})
	}
}

// TestProbeCountConstant is the white-box complexity proof across scales:
// probes for one lookup must stay a small constant, never grow with m.
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(1 << 40)
		batch := make([]Entry, m)
		for i := range batch {
			batch[i] = E(fmt.Sprintf("k%05d", i), int64(i))
		}
		v, err := s.Broadcast(batch)
		if err != nil {
			t.Fatal(err)
		}
		if val, kin, err := s.Lookup(v, fmt.Sprintf("k%05d", m-1)); err != nil ||
			kin != Found || val != int64(m-1) {
			t.Fatalf("m=%d: val=%d kin=%d err=%v", m, val, kin, err)
		}
		if p := s.lastProbe.Load(); p < 1 || p > 2 {
			t.Fatalf("m=%d probes=%d, want constant in [1,2]; a scan would be O(m)", m, p)
		}
	}
}
