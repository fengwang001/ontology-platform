package hint

import "testing"

// TestAppendO1ScanBounded pins "append is O(1), dedup is deferred to replay":
// after m buffered hints, one more Append must scan the same (zero) number of
// existing hints at every depth m. The counter is read white-box here; no
// exported API can reach it.
func TestAppendO1ScanBounded(t *testing.T) {
	depths := []int{100, 500, 1000, 5000, 10000}
	scans := make([]int, len(depths))
	for i, m := range depths {
		b := New(m + 1)
		for j := 0; j < m; j++ {
			b.Append(Entry{Key: "k", Ver: int64(j + 1)})
		}
		b.Append(Entry{Key: "k", Value: "last", Ver: int64(m + 1)})
		scans[i] = b.lastScan
	}
	for i := 1; i < len(scans); i++ {
		if scans[i] != scans[0] {
			t.Fatalf("scan count grew with depth: %v", scans)
		}
	}
	if scans[0] != 0 {
		t.Fatalf("Append scanned existing hints, want 0, got %d", scans[0])
	}
}

// TestBufferReplay is table-driven over in-order strict-> replay: only hints
// strictly newer than the per-key current version apply; stale and tied
// versions are skipped; the buffer drains and order is respected.
func TestBufferReplay(t *testing.T) {
	cases := []struct {
		name        string
		max         int
		seed        map[string]int64 // current version per key before replay
		hints       []Entry
		wantApplied int
		wantSkipped int
		wantVer     map[string]int64
		wantVal     map[string]string
	}{
		{
			name: "stale late version skipped, no downgrade",
			max:  4,
			seed: map[string]int64{},
			hints: []Entry{
				{Key: "k", Value: "a", Ver: 5},
				{Key: "k", Value: "b", Ver: 7},
				{Key: "k", Value: "c", Ver: 6},
			},
			wantApplied: 2, wantSkipped: 1,
			wantVer: map[string]int64{"k": 7}, wantVal: map[string]string{"k": "b"},
		},
		{
			name: "tie skipped, earliest winner kept",
			max:  3,
			seed: map[string]int64{"k": 9},
			hints: []Entry{
				{Key: "k", Value: "e", Ver: 9},
				{Key: "k", Value: "f", Ver: 9},
			},
			wantApplied: 0, wantSkipped: 2,
			wantVer: map[string]int64{"k": 9}, wantVal: map[string]string{"k": ""},
		},
		{
			name: "per-key versions tracked independently in order",
			max:  5,
			seed: map[string]int64{},
			hints: []Entry{
				{Key: "x", Value: "x1", Ver: 1},
				{Key: "y", Value: "y2", Ver: 2},
				{Key: "x", Value: "x3", Ver: 3},
				{Key: "y", Value: "y1", Ver: 1},
			},
			wantApplied: 3, wantSkipped: 1,
			wantVer: map[string]int64{"x": 3, "y": 2},
			wantVal: map[string]string{"x": "x3", "y": "y2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New(tc.max)
			store := map[string]Entry{}
			for k, v := range tc.seed {
				store[k] = Entry{Ver: v}
			}
			for _, e := range tc.hints {
				b.Append(e)
			}
			if b.Full() == false && len(tc.hints) == tc.max {
				t.Fatalf("Full() false at capacity")
			}
			gotA, gotS := b.Replay(
				func(k string) int64 { return store[k].Ver },
				func(e Entry) { store[e.Key] = e },
			)
			if gotA != tc.wantApplied || gotS != tc.wantSkipped {
				t.Fatalf("counts = (%d,%d), want (%d,%d)", gotA, gotS, tc.wantApplied, tc.wantSkipped)
			}
			if b.Len() != 0 {
				t.Fatalf("buffer not drained: %d left", b.Len())
			}
			for k, v := range tc.wantVer {
				if store[k].Ver != v || store[k].Value != tc.wantVal[k] {
					t.Fatalf("key %s = %+v, want ver %d val %q", k, store[k], v, tc.wantVal[k])
				}
			}
		})
	}
}

// TestApplyBoundary pins the strict > rule around equality.
func TestApplyBoundary(t *testing.T) {
	cases := []struct {
		v, cur int64
		want   bool
	}{{1, 0, true}, {5, 5, false}, {6, 5, true}, {4, 5, false}, {1, 1, false}}
	for _, c := range cases {
		if got := Apply(c.v, c.cur); got != c.want {
			t.Errorf("Apply(%d,%d) = %v, want %v", c.v, c.cur, got, c.want)
		}
	}
}
