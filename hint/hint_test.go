package hint

import "testing"

// TestAppendScanIsO1 proves appending never scans existing hints to dedup:
// across m = 100..10000 the unexported scan counter stays exactly 0, so
// Append is O(1) and stale/tie removal is deferred to replay.
func TestAppendScanIsO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		b := NewBuffer(m + 1)
		for i := 0; i < m; i++ {
			if err := b.Append(Entry{Key: "k", Ver: int64(i + 1)}); err != nil {
				t.Fatalf("m=%d: seed append: %v", m, err)
			}
		}
		if err := b.Append(Entry{Key: "k", Value: "last", Ver: int64(m + 1)}); err != nil {
			t.Fatalf("m=%d: final append: %v", m, err)
		}
		if b.scans != 0 {
			t.Fatalf("m=%d: append scanned %d existing hints, want 0 (O(1))", m, b.scans)
		}
		if got := b.Len(); got != m+1 {
			t.Fatalf("m=%d: Len=%d, want %d", m, got, m+1)
		}
	}
}

func TestBufferReplayTable(t *testing.T) {
	cases := []struct {
		name        string
		cap         int
		hints       []Entry
		wantApplied int
		wantSkipped int
		wantVal     string
		wantVer     int64
	}{
		{"strict newer chain", 4, []Entry{{Ver: 1, Value: "a"}, {Ver: 2, Value: "b"}, {Ver: 3, Value: "c"}}, 3, 0, "c", 3},
		{"late stale skipped", 4, []Entry{{Ver: 5, Value: "a"}, {Ver: 7, Value: "b"}, {Ver: 6, Value: "c"}}, 2, 1, "b", 7},
		{"tie skipped", 4, []Entry{{Ver: 9, Value: "e"}, {Ver: 9, Value: "f"}}, 1, 1, "e", 9},
		{"older only skipped", 4, []Entry{{Ver: 1, Value: "a"}, {Ver: 1, Value: "a2"}}, 1, 1, "a", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuffer(tc.cap)
			for _, h := range tc.hints {
				h.Key = "k"
				if err := b.Append(h); err != nil {
					t.Fatalf("append: %v", err)
				}
			}
			cur := Entry{}
			ap, sk := b.Replay(func(h Entry) bool {
				if !ShouldApply(h.Ver, cur.Ver) {
					return false
				}
				cur = h
				return true
			})
			if ap != tc.wantApplied || sk != tc.wantSkipped || cur.Value != tc.wantVal || cur.Ver != tc.wantVer {
				t.Fatalf("got (%d,%d,%s@%d), want (%d,%d,%s@%d)",
					ap, sk, cur.Value, cur.Ver, tc.wantApplied, tc.wantSkipped, tc.wantVal, tc.wantVer)
			}
			if b.Len() != 0 {
				t.Fatalf("buffer not cleared: %d", b.Len())
			}
		})
	}
}
