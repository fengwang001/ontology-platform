package hagg

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"testing"

	"ontology/hop"
)

// TestAdvanceCheckedBounded 钉住复杂度约束：检查个数不随 m 增长，关闭 c 个时 <= c+常数。
func TestAdvanceCheckedBounded(t *testing.T) {
	const base = int64(1) << 40
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			a := New(12, 4, 4*m)
			for i := 0; i < m; i++ { // 互不相同的 Key + 很大的 TS，窗口保持打开
				if err := a.Add(fmt.Sprintf("k%d", i), base+int64(i)*4); err != nil {
					t.Fatal(err)
				}
			}
			res, err := a.Advance(base - 1000) // 不关闭任何窗口
			if err != nil || len(res) != 0 {
				t.Fatalf("no-close advance: len=%d err=%v", len(res), err)
			}
			if a.checked > 2 {
				t.Fatalf("no-close advance checked %d windows with m=%d, want <= 2 (constant)", a.checked, m)
			}
			res, err = a.Advance(base + int64(m/2)*4) // 恰好关闭 c 个窗口
			if c := len(res); err != nil || c == 0 {
				t.Fatalf("closing advance: c=%d err=%v", c, err)
			} else if a.checked > c+2 {
				t.Fatalf("closing advance checked %d, closed %d, want <= c+2", a.checked, c)
			}
		})
	}
}

type ev struct { // 到达时时钟（MinInt64 表示负无穷）
	key       string
	ts, clock int64
}

func runScript(a *Agg, seed uint64, n int) []ev {
	var events []ev
	clock, x := int64(math.MinInt64), seed
	next := func(m int64) int64 { x = x*6364136223846793005 + 1442695040888963407; return int64(x>>33) % m }
	for i := 0; i < n; i++ {
		if next(5) < 3 {
			key, ts := "k"+string(rune('a'+next(7))), next(120)-60
			if a.Add(key, ts) == nil {
				events = append(events, ev{key, ts, clock})
			}
			continue
		}
		tm := max(next(50)-25, clock) // clock 初值 MinInt64 时不生效
		a.Advance(tm)
		clock = tm
	}
	return events
}

func naiveRef(events []ev, size, slide int64) []Result {
	m := map[[2]any]int64{}
	for _, e := range events {
		for _, s := range hop.Starts(e.ts, size, slide) {
			if s+size > e.clock {
				m[[2]any{e.key, s}]++
			}
		}
	}
	out := make([]Result, 0, len(m))
	for k, c := range m {
		out = append(out, Result{Key: k[0].(string), Start: k[1].(int64), End: k[1].(int64) + size, Count: c})
	}
	slices.SortFunc(out, byEndKey)
	return out
}
func countSum(rs []Result) (t int64) {
	for _, r := range rs {
		t += r.Count
	}
	return
}
func byEndKey(a, b Result) int {
	return cmp.Or(cmp.Compare(a.End, b.End), cmp.Compare(a.Key, b.Key), cmp.Compare(a.Start, b.Start))
}
func TestNaiveReference(t *testing.T) {
	cases := []struct {
		size, slide int64
		seed        uint64
		n           int
	}{
		{12, 4, 1, 300}, {12, 4, 7, 500}, {6, 2, 3, 400}, {5, 1, 9, 300}, {8, 4, 11, 400},
	}
	for _, tc := range cases {
		a := New(tc.size, tc.slide, 1<<20)
		events := runScript(a, tc.seed, tc.n)
		a.Flush()
		if got, want := a.Results(), naiveRef(events, tc.size, tc.slide); !slices.Equal(got, want) {
			t.Errorf("size=%d slide=%d seed=%d: mismatch vs naive reference", tc.size, tc.slide, tc.seed)
		}
	}
}
func TestAssignmentCounts(t *testing.T) {
	for _, tc := range []struct {
		size, slide int64
		events      int
	}{{12, 4, 60}, {6, 3, 40}, {10, 5, 25}} {
		a := New(tc.size, tc.slide, 1<<20)
		for i := 0; i < tc.events; i++ {
			a.Add("k", int64(i)*2-50)
		}
		if total, want := countSum(a.Flush()), int64(tc.events)*(tc.size/tc.slide); total != want {
			t.Errorf("size=%d: total %d != %d", tc.size, total, want)
		}
	}
	// 部分迟到计入恰好 end>clock 的窗口；全关才丢弃。
	a := New(12, 4, 1<<20)
	a.Add("k", -1)  // 计入 [-12,0) [-8,4) [-4,8)
	a.Advance(0)    // 关闭 [-12,0)
	a.Add("k", -3)  // 所属 3 个窗口中 [-12,0) 已关，计入其余 2 个
	a.Add("k", -13) // 所属窗口全已关，丢弃
	a.Flush()
	if total := countSum(a.Results()); total != 5 || a.Dropped() != 1 { // 含 Advance(0) 输出的 [-12,0):1
		t.Errorf("total=%d dropped=%d, want 5/1", total, a.Dropped())
	}
}
func TestOrderedUnique(t *testing.T) {
	a := New(12, 4, 1<<20)
	runScript(a, 5, 400)
	a.Flush()
	all := a.Results()
	if !slices.IsSortedFunc(all, byEndKey) {
		t.Fatal("output not ordered by (end, Key)")
	}
	sameWin := func(x, y Result) bool { return x.Key == y.Key && x.Start == y.Start }
	if len(slices.CompactFunc(all, sameWin)) != len(all) {
		t.Fatal("duplicate (Key, window) output")
	}
	// 关闭输出的 end 不超过推进到的时钟。
	a2 := New(12, 4, 1<<20)
	a2.Add("a", -5)
	out, _ := a2.Advance(0)
	if len(out) != 2 || out[0].End > 0 || out[1].End > 0 {
		t.Fatalf("advance(0) output: %v", out)
	}
}
