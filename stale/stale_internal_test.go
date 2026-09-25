package stale

import (
	"errors"
	"testing"
)

// TestAccessedCounterO1 is white-box on purpose: the access counter is
// unexported and must stay unreachable through any exported method. Feeding
// m data records then one trailing record must consult zero history entries
// regardless of m: stale is decided from O(1) scalars only.
func TestAccessedCounterO1(t *testing.T) {
	ms := []int{100, 500, 1000, 5000, 10000}
	for _, m := range ms {
		d := NewDetector(5)
		for seq := 1; seq <= m; seq++ {
			if err := d.Feed(Data{Seq: int64(seq), Val: 1}); err != nil {
				t.Fatalf("m=%d feed: %v", m, err)
			}
		}
		trailing := []struct {
			name string
			act  func() error
		}{
			{"watermark", func() error { return d.Feed(Watermark{UpTo: int64(m)}) }},
			{"tick", func() error { return d.Tick(int64(m)) }},
			{"heartbeat", func() error { return d.Feed(Heartbeat{}) }},
		}
		for _, tc := range trailing {
			if err := tc.act(); err != nil {
				t.Fatalf("m=%d %s: %v", m, tc.name, err)
			}
			// Must be 0 (a small m-independent constant would also prove
			// O(1); this implementation never stores history at all).
			if d.accessed != 0 {
				t.Fatalf("m=%d %s accessed=%d, want 0 (must not scale with m)",
					m, tc.name, d.accessed)
			}
		}
	}
}

// TestRejectedFeedLeavesNoTrace is also white-box: a rejected operation is
// still "handling one record", consults no history, and leaves state and
// view untouched.
func TestRejectedFeedLeavesNoTrace(t *testing.T) {
	d := NewDetector(5)
	if err := d.Feed(Data{Seq: 1, Val: 9}); err != nil {
		t.Fatal(err)
	}
	d.accessed = 7 // tamper: the next rejected call must reset it
	err := d.Feed(Data{Seq: 5, Val: 1})
	if !errors.Is(err, ErrDataGap) {
		t.Fatalf("want ErrDataGap, got %v", err)
	}
	if d.accessed != 0 || d.Applied() != 1 || d.View() != 9 || d.Stale() {
		t.Fatalf("rejected feed left a trace: accessed=%d A=%d view=%d stale=%t",
			d.accessed, d.Applied(), d.View(), d.Stale())
	}
}

// TestClockMonotonic (white-box) nails invariant 3 for now and lastHB, which
// the api layer does not expose: equal values are no-ops, rejected Tick and
// non-contiguous Data never move either clock backwards.
func TestClockMonotonic(t *testing.T) {
	d := NewDetector(5)
	seq := []func() error{
		func() error { return d.Tick(3) },
		func() error { return d.Tick(3) },
		func() error { return d.Feed(Heartbeat{}) },
		func() error { return d.Tick(7) },
		func() error { return d.Feed(Heartbeat{}) },
	}
	var pn, ph int64
	for _, f := range seq {
		if err := f(); err != nil {
			t.Fatal(err)
		}
		if d.now < pn || d.s.LastBeat() < ph {
			t.Fatalf("clock regressed: now %d<%d or lastHB %d<%d", d.now, pn, d.s.LastBeat(), ph)
		}
		pn, ph = d.now, d.s.LastBeat()
	}
	if err := d.Tick(6); !errors.Is(err, ErrTickBacktrack) || d.now != 7 {
		t.Fatalf("rejected tick changed clock: err=%v now=%d", err, d.now)
	}
}
