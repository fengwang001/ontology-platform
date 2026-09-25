package swin

import (
	"testing"

	"ontology/sess"
)

// TestLookupCostBinary is the white-box cost proof from the spec: with m
// disjoint open sessions and an event far below the earliest start, the
// merge-set comparison count must stay within a small constant independent
// of m, i.e. lookup is binary, not a scan.
func TestLookupCostBinary(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e, err := NewEngine(3, m+1)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		ks := &keyState{}
		for i := 0; i < m; i++ { // spacing 2*gap: sessions never merge
			ts := int64(1000) + 6*int64(i)
			ks.ss = append(ks.ss, sess.Session{Start: ts, End: ts, Count: 1})
		}
		e.keys["K"] = ks
		e.cmps = 0
		if err := e.Feed([]Event{{Key: "K", TS: 0}}); err != nil {
			t.Fatalf("m=%d: feed: %v", m, err)
		}
		if e.cmps > 2 {
			t.Fatalf("m=%d: compared %d sessions, want <= 2 (binary lookup)", m, e.cmps)
		}
	}
}

// TestEngineSteps table-drives per-step expectations on key K.
func TestEngineSteps(t *testing.T) {
	cases := []struct {
		name string
		ts   []int64
		want []sess.Session
		drop int
	}{
		{"notes-eight", []int64{10, 13, 20, 16, 23, 17, 25, 11},
			[]sess.Session{
				{Start: 10, End: 13, Count: 2, Closed: true},
				{Start: 17, End: 25, Count: 4},
			}, 2},
		{"alt-order", []int64{20, 16, 10, 13, 23, 17, 25, 11},
			[]sess.Session{
				{Start: 10, End: 10, Count: 1, Closed: true},
				{Start: 16, End: 16, Count: 1, Closed: true},
				{Start: 20, End: 25, Count: 3},
			}, 3},
		{"reverse-only", []int64{20, 23, 17},
			[]sess.Session{{Start: 17, End: 23, Count: 3}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := NewEngine(3, 100)
			for _, ts := range tc.ts {
				if err := e.Feed([]Event{{Key: "K", TS: ts}}); err != nil {
					t.Fatal(err)
				}
			}
			got := e.View()["K"]
			if len(got) != len(tc.want) {
				t.Fatalf("sessions = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("row %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
			if e.Dropped() != tc.drop {
				t.Fatalf("dropped = %d, want %d", e.Dropped(), tc.drop)
			}
		})
	}
}
