package query

import (
	"math/bits"
	"sync/atomic"
	"testing"

	"ontology/head"
)

func mustHead(t *testing.T, l, ooo int64, smax, lim int) *head.Head {
	t.Helper()
	h, err := head.New(l, ooo, smax, lim)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func check(t *testing.T, q *Querier, name string, ts int64, want Result) {
	t.Helper()
	got := q.Instant(name, ts)
	if got != want {
		t.Fatalf("Instant(%q,%d) = %v, 期望 %v", name, ts, got, want)
	}
	t.Logf("输入 Instant(%q,%d) 输出 %v 判定 %v", name, ts, got, want)
}

// 回看窗口恰等 L 不可见、差 1 可见；陈旧标记；序列不存在为 Absent。
func TestInstantLookback(t *testing.T) {
	h := mustHead(t, 300, 100, 10, 10)
	q := New(h)
	if err := h.Append("m", 1000, 7); err != nil {
		t.Fatal(err)
	}
	if err := h.AppendStale("m", 1200); err != nil {
		t.Fatal(err)
	}
	check(t, q, "m", 999, Result{Kind: Absent})       // 首个样本之前
	check(t, q, "m", 1000, Result{Kind: Value, V: 7}) // 差 0
	check(t, q, "m", 1199, Result{Kind: Value, V: 7}) // 差 199 < L
	check(t, q, "m", 1200, Result{Kind: Stale})       // 标记本时刻
	check(t, q, "m", 1499, Result{Kind: Stale})       // 差 299 < L
	check(t, q, "m", 1500, Result{Kind: Absent})      // 差恰等 L=300
	check(t, q, "m", 1600, Result{Kind: Absent})      // 差 400 > L
	check(t, q, "ghost", 1200, Result{Kind: Absent})  // 序列不存在
	check(t, q, "m", -1, Result{Kind: Absent})        // t 非法
	check(t, q, "m", 1e12+1, Result{Kind: Absent})    // t 非法
}

// 多个序列同一时刻取值。
func TestInstantMany(t *testing.T) {
	h := mustHead(t, 300, 100, 10, 10)
	q := New(h)
	_ = h.Append("a", 100, 1)
	_ = h.AppendStale("b", 100)
	got := q.InstantMany([]string{"a", "b", "c"}, 200)
	want := []Result{{Kind: Value, V: 1}, {Kind: Stale}, {Kind: Absent}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("InstantMany[%d] = %v, 期望 %v", i, got[i], want[i])
		}
	}
	t.Logf("输入 InstantMany([a b c],200) 输出 %v", got)
}

// 二分查找考察样本数 ≤ ⌈log2(n+1)⌉+1, n 取 100 与 100000。
func TestInstantBinarySearchProbes(t *testing.T) {
	for _, n := range []int{100, 100000} {
		h := mustHead(t, 1e9, 0, n+1, 10)
		items := make([]head.Item, n)
		for i := range items {
			items[i] = head.Item{Series: "s", Ts: int64(i), V: int64(i)}
		}
		if err := h.ApplyBatch(items); err != nil {
			t.Fatal(err)
		}
		q := New(h)
		limit := int64(bits.Len(uint(n)) + 1) // ⌈log2(n+1)⌉+1
		for _, ts := range []int64{0, int64(n) / 2, int64(n - 1), int64(n) + 5} {
			atomic.StoreInt64(&probes, 0)
			q.Instant("s", ts)
			got := atomic.LoadInt64(&probes)
			if got > limit {
				t.Fatalf("n=%d t=%d 考察 %d 个样本, 超过上限 %d", n, ts, got, limit)
			}
			t.Logf("n=%d t=%d 考察样本数=%d 上限=%d", n, ts, got, limit)
		}
	}
}
