package store

import (
	"maps"
	"strconv"
	"testing"
)

// Invariant 1: a key's row/tombstone version always equals the max applied
// Ver; ignored events change nothing. R is huge so no purge interferes.
func TestInvariantMonotonic(t *testing.T) {
	seqs := map[string][]Event{
		"up-downgrade-delete-equal": {
			{Op: 'U', Key: "a", Val: "x", Ver: 5}, {Op: 'U', Key: "a", Val: "y", Ver: 3},
			{Op: 'D', Key: "a", Ver: 7}, {Op: 'U', Key: "a", Val: "z", Ver: 7},
		},
		"delete-then-older-upserts": {
			{Op: 'D', Key: "b", Ver: 9}, {Op: 'U', Key: "b", Val: "v", Ver: 4},
			{Op: 'U', Key: "b", Val: "w", Ver: 9},
		},
	}
	for name, seq := range seqs {
		t.Run(name, func(t *testing.T) {
			s := New(1<<60, 100)
			cur := map[string]int64{}
			ignored := 0
			for _, e := range seq {
				if err := s.Batch([]Event{e}); err != nil {
					t.Fatal(err)
				}
				if e.Ver > cur[e.Key] {
					cur[e.Key] = e.Ver
				} else {
					ignored++
				}
				for k, want := range cur {
					got := s.tombs[k]
					if r, ok := s.rows[k]; ok {
						got = r.Ver
					}
					if got != want {
						t.Fatalf("key %s: version %d, want max applied %d", k, got, want)
					}
				}
				if s.ignored != int64(ignored) {
					t.Fatalf("ignored = %d, want %d", s.ignored, ignored)
				}
			}
		})
	}
}

// Invariant 3: while a tombstone exists, upserts with Ver <= tomb version are
// ignored and never resurrect the key.
func TestTombstoneBlocks(t *testing.T) {
	cases := []struct{ delVer, upVer int64; blocked bool }{
		{8, 6, true}, {8, 8, true}, {8, 9, false},
	}
	for _, c := range cases {
		s := New(1<<60, 10)
		if err := s.Batch([]Event{{Op: 'D', Key: "k", Ver: c.delVer}}); err != nil {
			t.Fatal(err)
		}
		if err := s.Batch([]Event{{Op: 'U', Key: "k", Val: "x", Ver: c.upVer}}); err != nil {
			t.Fatal(err)
		}
		_, live := s.rows["k"]
		if live == c.blocked {
			t.Fatalf("del@%d up@%d: live=%v, want blocked=%v", c.delVer, c.upVer, live, c.blocked)
		}
		if tv, ok := s.tombs["k"]; c.blocked && (!ok || tv != c.delVer) {
			t.Fatalf("del@%d up@%d: tombstone (%d,%v), want (%d,true)", c.delVer, c.upVer, tv, ok, c.delVer)
		}
	}
}

// Invariant 4: a rejected batch (invalid event or overflow) changes nothing,
// and the store keeps working afterwards.
func TestRejectionLeavesNoTrace(t *testing.T) {
	bads := map[string]struct {
		bad  []Event
		want error
	}{
		"bad-op":        {[]Event{{Op: '?', Key: "b", Ver: 2}}, ErrInvalidEvent},
		"empty-key":     {[]Event{{Op: 'U', Key: "", Ver: 2}}, ErrInvalidEvent},
		"zero-version":  {[]Event{{Op: 'U', Key: "b", Ver: 0}}, ErrInvalidEvent},
		"overflow":      {[]Event{{Op: 'U', Key: "b", Ver: 2}, {Op: 'U', Key: "c", Ver: 3}}, ErrTooManyKeys},
		"valid-then-bad": {[]Event{{Op: 'U', Key: "b", Ver: 2}, {Op: '?', Key: "c", Ver: 3}}, ErrInvalidEvent},
	}
	for name, tc := range bads {
		t.Run(name, func(t *testing.T) {
			s := New(1<<60, 2)
			if err := s.Batch([]Event{{Op: 'U', Key: "a", Val: "1", Ver: 1}}); err != nil {
				t.Fatal(err)
			}
			rows, tombs := maps.Clone(s.rows), maps.Clone(s.tombs)
			g, ign, chk := s.g, s.ignored, s.checked
			if err := s.Batch(tc.bad); err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !maps.Equal(s.rows, rows) || !maps.Equal(s.tombs, tombs) ||
				s.g != g || s.ignored != ign || s.checked != chk {
				t.Fatal("rejected batch left a state change")
			}
			if err := s.Batch([]Event{{Op: 'U', Key: "a", Val: "2", Ver: 5}}); err != nil {
				t.Fatalf("store unusable after rejection: %v", err)
			}
		})
	}
}

// The purge inspection counter stays O(1) in the tombstone count m, and
// equals purged+discarded plus at most one live-root check otherwise.
func TestPurgeInspection(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(1<<60, m+2)
		evs := make([]Event, 0, m)
		for i := 1; i <= m; i++ {
			evs = append(evs, Event{Op: 'D', Key: strconv.Itoa(i), Ver: int64(i)})
		}
		if err := s.Batch(evs); err != nil {
			t.Fatal(err)
		}
		if err := s.Batch([]Event{{Op: 'U', Key: "p", Ver: int64(m + 1)}}); err != nil {
			t.Fatal(err)
		}
		if s.checked > 1 {
			t.Fatalf("m=%d: inspected %d tombstones, want O(1)", m, s.checked)
		}
	}
	// All five tombstones expire: exactly five inspections, no extra scan.
	s := New(10, 100)
	for i := 1; i <= 5; i++ {
		if err := s.Batch([]Event{{Op: 'D', Key: strconv.Itoa(i), Ver: int64(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Batch([]Event{{Op: 'U', Key: "p", Ver: 20}}); err != nil {
		t.Fatal(err)
	}
	if s.checked != 5 || len(s.tombs) != 0 {
		t.Fatalf("checked = %d, tombs = %d; want 5, 0", s.checked, len(s.tombs))
	}
	// A stale heap entry (superseded tombstone) is discarded, not scanned.
	s2 := New(10, 100)
	for _, e := range []Event{
		{Op: 'D', Key: "x", Ver: 1}, {Op: 'D', Key: "x", Ver: 2}, {Op: 'U', Key: "p", Ver: 20},
	} {
		if err := s2.Batch([]Event{e}); err != nil {
			t.Fatal(err)
		}
	}
	if s2.checked != 2 || len(s2.tombs) != 0 {
		t.Fatalf("checked = %d, tombs = %d; want 2, 0", s2.checked, len(s2.tombs))
	}
}
