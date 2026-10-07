package precheck

import "testing"

// Test temporal boundary semantics: new segment takes effect exactly at its start.

func TestTemporalPointBoundary(t *testing.T) {
	store := NewTemporalStore[string]()
	store.Put("k", 10, "v1")
	store.Put("k", 20, "v2")

	cases := []struct {
		at      Moment
		want    string
		present bool
	}{
		{9, "", false},
		{10, "v1", true},
		{19, "v1", true},
		{20, "v2", true},
		{100, "v2", true},
	}
	for _, c := range cases {
		got, ok, gap := store.Point("k", c.at)
		if gap || ok != c.present || got != c.want {
			t.Fatalf("at=%d got=(%q,%v,gap=%v), want (%q,%v)", c.at, got, ok, gap, c.want, c.present)
		}
	}
}

func TestTemporalRemoveAndGap(t *testing.T) {
	store := NewTemporalStore[int]()
	store.Put("k", 0, 1)
	store.Remove("k", 5)
	store.Put("k", 10, 2)

	if _, ok, _ := store.Point("k", 4); !ok {
		t.Fatal("expected value at 4")
	}
	if _, ok, _ := store.Point("k", 5); ok {
		t.Fatal("expected absent at 5 (removal boundary)")
	}
	if _, ok, _ := store.Point("k", 9); ok {
		t.Fatal("expected absent at 9")
	}
	if v, ok, _ := store.Point("k", 10); !ok || v != 2 {
		t.Fatalf("expected 2 at 10, got %v %v", v, ok)
	}

	store.Gap("k", 6, 8)
	if _, _, gap := store.Point("k", 5); gap {
		t.Fatal("gap must be half-open: 5 not in [6,8)")
	}
	if _, _, gap := store.Point("k", 6); !gap {
		t.Fatal("6 must be a gap")
	}
	if _, _, gap := store.Point("k", 7); !gap {
		t.Fatal("7 must be a gap")
	}
	if _, _, gap := store.Point("k", 8); gap {
		t.Fatal("8 must not be a gap (right-open)")
	}
}

func TestTemporalOutOfOrderPanics(t *testing.T) {
	store := NewTemporalStore[string]()
	store.Put("k", 10, "a")
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on out-of-order write")
		}
	}()
	store.Put("k", 9, "b")
}

// TestTemporalSegmentChecksBounded 验证点查的段检查次数随段数仅对数增长，
// 即单次快照重建不随累计版本演进次数线性增长（可独立核查的计数器）。
func TestTemporalSegmentChecksBounded(t *testing.T) {
	store := NewTemporalStore[int]()
	for i := 0; i < 100_000; i++ {
		store.Put("hot", Moment(i), i)
	}
	store.ResetStats()
	_, _, _ = store.Point("hot", 99_999)
	stats := store.Stats()
	if stats.PointCalls != 1 {
		t.Fatalf("want 1 point call, got %d", stats.PointCalls)
	}
	// log2(100000) ~ 17，留足余量取 30；线性扫描将高达 100000。
	if stats.SegmentChecks > 30 {
		t.Fatalf("segment checks %d indicate linear scan, expected O(log n)", stats.SegmentChecks)
	}
}
