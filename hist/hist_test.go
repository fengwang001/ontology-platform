package hist

import (
	"math/bits"
	"sort"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// buildNine 构造 NOTES 第三节的九个事件。
func buildNine(t *testing.T) *History {
	t.Helper()
	h, err := New(3, 100)
	must(t, err)
	steps := []struct{ kind, a, b int }{
		{0, 3, 0}, {1, 3, 1}, {0, 2, 0}, {2, 1, 1}, {1, 1, 2},
		{1, 2, 3}, {2, 2, 2}, {2, 3, 3}, {0, 1, 0},
	}
	for _, s := range steps {
		switch s.kind {
		case 0:
			err = h.Local(s.a)
		case 1:
			_, _, err = h.Send(s.a, s.b)
		case 2:
			_, err = h.Recv(s.b)
		}
		must(t, err)
	}
	return h
}

// TestNineEvents 钉住第三节推导：九个时间戳、全序、e6/e4 并发、并发对计数。
func TestNineEvents(t *testing.T) {
	h := buildNine(t)
	ord := h.Order()
	wantTS := []int{1, 2, 1, 3, 4, 2, 5, 3, 5}
	wantOrder := []int{3, 1, 6, 2, 4, 8, 5, 9, 7}
	for i, e := range ord {
		if e.TS != wantTS[e.ID-1] || e.ID != wantOrder[i] {
			t.Fatalf("position %d: %+v", i+1, e)
		}
	}
	if h.HappensBefore(6, 4) || h.HappensBefore(4, 6) {
		t.Fatal("e6 and e4 must be concurrent")
	}
	hb, conc := 0, 0 // 36 个有序对中 HB 21 对、并发 15 对
	for a := 1; a <= 9; a++ {
		for b := a + 1; b <= 9; b++ {
			ab, ba := h.HappensBefore(a, b), h.HappensBefore(b, a)
			if ab && ba {
				t.Fatalf("e%d,e%d mutually happens-before", a, b)
			}
			if ab || ba {
				hb++
			} else {
				conc++
			}
		}
	}
	if hb != 21 || conc != 15 {
		t.Fatalf("hb=%d conc=%d, want 21/15", hb, conc)
	}
}

func TestOrderMatchesBatchSort(t *testing.T) {
	got := buildNine(t).Order()
	batch := append([]Event(nil), got...)
	sort.Slice(batch, func(i, j int) bool {
		if batch[i].TS != batch[j].TS {
			return batch[i].TS < batch[j].TS
		}
		return batch[i].Node < batch[j].Node
	})
	for i := range got {
		if got[i] != batch[i] {
			t.Fatalf("position %d: incremental order %+v != batch %+v", i, got, batch)
		}
	}
}

func TestClockCondition(t *testing.T) {
	h := buildNine(t)
	byID := make([]Event, 10)
	for _, e := range h.Order() {
		byID[e.ID] = e
	}
	for a := 1; a <= 9; a++ {
		for b := 1; b <= 9; b++ {
			if h.HappensBefore(a, b) && !(byID[a].TS < byID[b].TS) {
				t.Fatalf("clock condition violated: e%d(ts=%d) -> e%d(ts=%d)",
					a, byID[a].TS, b, byID[b].TS)
			}
		}
	}
}

func TestStrictTotalOrder(t *testing.T) {
	seen := map[[2]int]bool{}
	last := map[int]int{}
	for _, e := range buildNine(t).Order() {
		k := [2]int{e.TS, e.Node}
		if seen[k] {
			t.Fatalf("duplicate order key %v", k)
		}
		seen[k] = true
		if t2, ok := last[e.Node]; ok && !(t2 < e.TS) {
			t.Fatalf("node %d timestamps not strictly increasing: %d then %d", e.Node, t2, e.TS)
		}
		last[e.Node] = e.TS
	}
}

// TestInsertComparisonsBounded 证明插入按有序结构定位：m 个事件后再插一个落在
// 全序中段的事件，比较次数 ≤ 2⌈log₂(m+1)⌉+2，不随 m 线性增长。
func TestInsertComparisonsBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		h, err := New(3, m+2)
		must(t, err)
		for i := 0; i < m/5; i++ { // 节点 1 得 m/5 个，节点 2、3 各得 2m/5 个
			for _, nd := range [5]int{1, 2, 3, 2, 3} {
				must(t, h.Local(nd))
			}
		}
		must(t, h.Local(1))              // 新键 (m/5+1,1) 落在全序中段
		bound := 2*bits.Len(uint(m)) + 2 // bits.Len(m) == ⌈log₂(m+1)⌉
		if h.lastCmp < 1 || h.lastCmp > bound {
			t.Fatalf("m=%d: comparisons %d exceed bound %d", m, h.lastCmp, bound)
		}
		pos := -1
		for i, e := range h.Order() {
			if e.ID == m+1 {
				pos = i
			}
		}
		if pos < m/4 || pos > 3*m/4 {
			t.Fatalf("m=%d: new event at position %d, not in the middle", m, pos)
		}
	}
}
