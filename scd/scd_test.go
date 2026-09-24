package scd

import (
	"fmt"
	"math/bits"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/interval"
)

// wellFormed 校验历史行良构：from<to、按 from 升序、两两不重叠。
func wellFormed(rows []interval.Row) bool {
	for i, r := range rows {
		if !(r.From < r.To) || (i > 0 && rows[i-1].To > r.From) {
			return false
		}
	}
	return true
}

// TestBinarySearchComparisonCount 钉住第四节：多档 m 下乱序定位的比较数
// 不随 m 线性增长，满足 2*ceil(log2 m)+4，证明用的是二分而非线性扫描。
func TestBinarySearchComparisonCount(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		tb := NewTable(m + 1)
		seed := make([]Event, m)
		for k := range seed { // 顺序喂入 m 个偶数 Eff 的 Upsert
			seed[k] = Event{Key: "M", Point: interval.Point{Eff: int64(2 * k), Val: "x"}}
		}
		if err := tb.Apply(seed); err != nil {
			t.Fatalf("m=%d seed: %v", m, err)
		}
		tb.cmp = 0 // 只统计下一条乱序事件的定位比较
		mid := Event{Key: "M", Point: interval.Point{Eff: int64(m + 1), Val: "y"}}
		if err := tb.Apply([]Event{mid}); err != nil {
			t.Fatalf("m=%d mid: %v", m, err)
		}
		bound := 2*bits.Len(uint(m-1)) + 4 // 2*ceil(log2 m)+4
		if tb.cmp > bound {
			t.Fatalf("m=%d comparisons=%d > bound=%d (linear scan?)", m, tb.cmp, bound)
		}
		if tb.cmp >= m { // 额外保证：明显小于点数，排除线性扫描
			t.Fatalf("m=%d comparisons=%d not sub-linear", m, tb.cmp)
		}
	}
}

// TestOrderIndependence 钉住不变量 3：Eff 两两不同的事件以多种随机排列
// 喂入，历史都相同且等于排序后一次性 Build 的结果。
func TestOrderIndependence(t *testing.T) {
	for seed := int64(0); seed < 12; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 60
		evs := make([]Event, n)
		pts := make([]interval.Point, n)
		for i := 0; i < n; i++ {
			p := interval.Point{Eff: int64(2*i + 1), Del: rng.Intn(2) == 0, Val: fmt.Sprint(i)}
			evs[i], pts[i] = Event{Key: "P", Point: p}, p
		}
		sort.Slice(pts, func(a, b int) bool { return pts[a].Eff < pts[b].Eff })
		want := interval.Build(pts)
		rng.Shuffle(len(evs), func(i, j int) { evs[i], evs[j] = evs[j], evs[i] })
		tb := NewTable(1000)
		if err := tb.Apply(evs); err != nil {
			t.Fatalf("seed=%d apply: %v", seed, err)
		}
		if got := tb.History("P"); !reflect.DeepEqual(got, want) || !wellFormed(got) {
			t.Fatalf("seed=%d order-dependent or ill-formed: %v", seed, got)
		}
	}
}

// TestWellFormed 钉住不变量 2：随机（含重复 Eff 替换、乱序、Delete）逐步
// 喂入后，历史始终良构，且与权威点集一次性 Build 逐行相同（兼验不变量 1）。
func TestWellFormed(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tb := NewTable(100000)
	for step := 0; step < 3000; step++ {
		e := Event{Key: "W", Point: interval.Point{
			Eff: rng.Int63n(400), Del: rng.Intn(2) == 0, Val: fmt.Sprint(rng.Intn(10))}}
		if err := tb.Apply([]Event{e}); err != nil {
			t.Fatalf("step=%d: %v", step, err)
		}
		rows := tb.History("W")
		if !wellFormed(rows) {
			t.Fatalf("step=%d ill-formed rows=%v", step, rows)
		}
		ks := tb.states["W"]
		if got := interval.Build(ks.points); !reflect.DeepEqual(rows, got) {
			t.Fatalf("step=%d rows diverge from points", step)
		}
		if _, ok := tb.AsOf("W", rng.Int63n(400)); ok {
			// AsOf 至多命中一行已由良构（不重叠）保证；此处仅确保不 panic。
		}
	}
}
