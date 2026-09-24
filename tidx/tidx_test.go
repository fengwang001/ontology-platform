package tidx

import "testing"

// TestProbeBound 证明按索引二分定位：探查数不随 m 线性增长。
func TestProbeBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		var ix Index
		for i := 0; i < m; i++ {
			ix.Add(int64(2*i), int64(i)) // 严格递增，索引恰有 m 项
		}
		if got := len(ix.Entries()); got != m {
			t.Fatalf("m=%d: index has %d entries", m, got)
		}
		ceil := 0
		for v := m + 1; v > 1; v = (v + 1) / 2 {
			ceil++
		}
		bound := 2*ceil + 4
		// 小于全部、大于全部、恰好命中、落在两项之间
		for _, q := range []int64{-1, int64(2 * m), int64(2 * (m / 2)), int64(2*(m/2) + 1)} {
			ix.Lookup(q)
			if p := ix.probes.Load(); p > int64(bound) {
				t.Errorf("m=%d t=%d: probes=%d > bound=%d (linear scan?)", m, q, p, bound)
			}
		}
	}
}

// TestIndexRules 钉住索引追加规则：严格大于迄今最大才记，等于不记。
func TestIndexRules(t *testing.T) {
	cases := []struct {
		name string
		ts   []int64
		want []Entry
	}{
		{"first always recorded", []int64{5}, []Entry{{5, 10}}},
		{"equal to max skipped", []int64{5, 5, 6, 6}, []Entry{{5, 10}, {6, 12}}},
		{"non-monotonic", []int64{50, 40, 70, 60, 70, 65, 90, 80, 90, 85},
			[]Entry{{50, 100}, {70, 102}, {90, 106}}},
		{"strictly increasing", []int64{1, 2, 3}, []Entry{{1, 0}, {2, 1}, {3, 2}}},
		{"strictly decreasing", []int64{9, 5, 1}, []Entry{{9, 0}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ix Index
			base := int64(0)
			if c.name == "non-monotonic" {
				base = 100
			} else if c.name == "first always recorded" || c.name == "equal to max skipped" {
				base = 10
			}
			for i, ts := range c.ts {
				ix.Add(ts, base+int64(i))
			}
			got := ix.Entries()
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
			for i := 1; i < len(got); i++ { // 严格单调
				if got[i].TS <= got[i-1].TS || got[i].Off <= got[i-1].Off {
					t.Fatalf("index not strictly increasing: %v", got)
				}
			}
		})
	}
}

// TestLookupOnIndex 直接对索引核验二分定位结果。
func TestLookupOnIndex(t *testing.T) {
	var ix Index
	for i, ts := range []int64{50, 40, 70, 60, 70, 65, 90, 80, 90, 85} {
		ix.Add(ts, int64(100+i))
	}
	cases := []struct {
		q     int64
		off   int64
		found bool
	}{
		{45, 100, true}, {50, 100, true}, {65, 102, true}, {70, 102, true},
		{71, 106, true}, {85, 106, true}, {90, 106, true}, {91, 0, false},
	}
	for _, c := range cases {
		off, found := ix.Lookup(c.q)
		if found != c.found || (found && off != c.off) {
			t.Errorf("Lookup(%d) = (%d,%v), want (%d,%v)", c.q, off, found, c.off, c.found)
		}
	}
	var empty Index
	if _, found := empty.Lookup(0); found {
		t.Error("empty index must not find")
	}
	if !ix.SelfCheck() {
		t.Error("Index.SelfCheck failed")
	}
}
