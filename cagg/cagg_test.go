package cagg

import (
	"fmt"
	"testing"
)

// TestScanBound proves the watermark advance locates due windows by the
// ordered next-end heap rather than scanning all open windows: with m open
// windows and an advance that fires none, the inspected count must stay a
// constant independent of m; when windows do fire it equals that firing
// count (distinct windows), i.e. constant + windows-actually-firing.
func TestScanBound(t *testing.T) {
	ms := []int{100, 1000, 10000}
	prev := -1
	for _, m := range ms {
		a, err := New(1, 1, 1<<60, m+1)
		if err != nil {
			t.Fatal(err)
		}
		evs := make([]Event, m) // huge delay: the batch opens m windows, fires none
		for i := range evs {
			evs[i] = Event{Key: fmt.Sprintf("k%d", i), TS: int64(i)}
		}
		if _, err := a.Feed(evs); err != nil {
			t.Fatal(err)
		}
		if got := len(a.wins); got != m {
			t.Fatalf("m=%d open windows=%d, want %d", m, got, m)
		}
		a.scanCnt = 0
		// probe moves the watermark by 1 but cannot fire any sub-window.
		if _, err := a.Feed([]Event{{Key: "probe", TS: int64(m)}}); err != nil {
			t.Fatal(err)
		}
		if a.scanCnt != 0 { // 0 fire => cost must be the constant 0, not O(m)
			t.Fatalf("m=%d scanCnt=%d, want 0", m, a.scanCnt)
		}
		if prev >= 0 && a.scanCnt != prev {
			t.Fatalf("scanCnt grew with m: %d then %d", prev, a.scanCnt)
		}
		prev = a.scanCnt

		// Control: an advance that fires windows reports exactly the
		// number of distinct windows firing (here two, end 2 tie).
		b, _ := New(2, 2, 0, 10)
		b.Feed([]Event{{Key: "A", TS: 0}, {Key: "B", TS: 0}})
		b.scanCnt = 0
		out, _ := b.Feed([]Event{{Key: "A", TS: 2}})
		if b.scanCnt != 2 { // firing advance reports exactly windows firing
			t.Fatalf("firing scanCnt=%d want 2", b.scanCnt)
		}
		if len(out) != 2 {
			t.Fatalf("firing outputs=%d want 2", len(out))
		}
	}
}

// TestScanBoundTable is the table-driven form across parameter shapes.
func TestScanBoundTable(t *testing.T) {
	cases := []struct {
		max, step, delay int64
		m                int
	}{
		{1, 1, 1 << 40, 100},
		{4, 4, 1 << 50, 500},
		{1, 1, 1 << 60, 5000},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("m=%d", c.m), func(t *testing.T) {
			a, _ := New(c.max, c.step, c.delay, c.m+1)
			evs := make([]Event, c.m)
			for i := range evs {
				evs[i] = Event{Key: fmt.Sprintf("k%d", i), TS: int64(i) * c.max}
			}
			if _, err := a.Feed(evs); err != nil {
				t.Fatal(err)
			}
			a.scanCnt = 0
			if _, err := a.Feed([]Event{{Key: "probe", TS: int64(c.m) * c.max}}); err != nil {
				t.Fatal(err)
			}
			if a.scanCnt != 0 {
				t.Fatalf("scanCnt=%d want 0 (no sub-window fired)", a.scanCnt)
			}
		})
	}
}
