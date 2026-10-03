package query

import (
	"math/bits"
	"testing"

	"ontology/head"
)

func mustQuerier(t *testing.T, l, ooo int64) (*Querier, *head.Head) {
	t.Helper()
	h, err := head.NewHead(l, ooo, 1e6, 1e5)
	if err != nil {
		t.Fatal(err)
	}
	return New(h), h
}

func TestInstantWindowBoundary(t *testing.T) {
	q, h := mustQuerier(t, 300, 0)
	if err := h.Append("m", 1000, 7); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		t    int64
		want Result
	}{
		{999, Result{Kind: Absent}},       // t 早于首个样本
		{1000, Result{Kind: Value, V: 7}}, // 恰在样本时刻
		{1299, Result{Kind: Value, V: 7}}, // 差 299 < L
		{1300, Result{Kind: Absent}},      // 差恰等 L=300，不可见
		{1301, Result{Kind: Absent}},      // 差 301 > L
	}
	for _, c := range cases {
		got := q.Instant("m", c.t)
		t.Logf("输入 Instant(m,%d) 输出 %+v 期望 %+v", c.t, got, c.want)
		if got != c.want {
			t.Fatalf("Instant(m,%d): 期望 %+v，得到 %+v", c.t, c.want, got)
		}
	}
	if got := q.Instant("missing", 1000); got.Kind != Absent {
		t.Fatalf("序列不存在应为 Absent，得到 %+v", got)
	}
	t.Logf("判定依据: 最新样本 s 满足 ts<=t；t-s.ts>=L（恰等不可见）为 Absent")
}

func TestInstantStaleMarker(t *testing.T) {
	q, h := mustQuerier(t, 300, 0)
	if err := h.Append("m", 1000, 7); err != nil {
		t.Fatal(err)
	}
	if err := h.AppendStale("m", 1200); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		t    int64
		want Result
	}{
		{1199, Result{Kind: Value, V: 7}}, // 标记前的旧值仍在窗口内
		{1200, Result{Kind: Stale}},       // 标记时刻起为 Stale
		{1499, Result{Kind: Stale}},       // 差 299 < L，标记仍可见
		{1500, Result{Kind: Absent}},      // 差恰等 L，标记也不可见
	}
	for _, c := range cases {
		if got := q.Instant("m", c.t); got != c.want {
			t.Fatalf("Instant(m,%d): 期望 %+v，得到 %+v", c.t, c.want, got)
		}
	}
	t.Logf("判定依据: 窗口内最新样本是陈旧标记则返回 Stale；标记同样受窗口约束")
}

func TestInstantMany(t *testing.T) {
	q, h := mustQuerier(t, 300, 0)
	_ = h.Append("a", 100, 1)
	_ = h.Append("b", 100, 2)
	_ = h.AppendStale("b", 200)
	got := q.InstantMany([]string{"a", "b", "c"}, 250)
	want := []Result{{Kind: Value, V: 1}, {Kind: Stale}, {Kind: Absent}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("InstantMany[%d]: 期望 %+v，得到 %+v", i, want[i], got[i])
		}
	}
	t.Logf("输入 [a,b,c]@250 输出 %+v；依据: 同一时刻各序列独立按窗口规则取值", got)
}

// TestSearchBound 断言二分查找考察样本数不超过 ⌈log2(n+1)⌉+1。
func TestSearchBound(t *testing.T) {
	for _, n := range []int{100, 100000} {
		q, h := mustQuerier(t, 1e9, 0)
		for i := 0; i < n; i++ {
			if err := h.Append("s", int64(i), int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		bound := int64(bits.Len(uint(n))) + 1 // ⌈log2(n+1)⌉+1
		var worst int64
		step := n/97 + 1
		for tt := -1; tt <= n+1; tt += step {
			examined.Store(0)
			got := q.Instant("s", int64(tt))
			if tt >= 0 && tt < n && got.Kind != Value {
				t.Fatalf("n=%d t=%d: 期望 Value，得到 %+v", n, tt, got)
			}
			if e := examined.Load(); e > worst {
				worst = e
			}
		}
		t.Logf("n=%d: 考察样本数最大值=%d，上界=%d；依据: ⌈log2(n+1)⌉+1", n, worst, bound)
		if worst > bound {
			t.Fatalf("n=%d: 考察样本数 %d 超过上界 %d", n, worst, bound)
		}
	}
}
