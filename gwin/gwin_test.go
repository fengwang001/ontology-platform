package gwin

import (
	"fmt"
	"math/rand"
	"testing"
)

// 表驱动：含第三节序列、负整数、随机顺序（固定种子循环生成）。
func recomputeCases() []struct {
	name   string
	period int64
	vals   []int64
} {
	cases := []struct {
		name   string
		period int64
		vals   []int64
	}{
		{"eight", 3, []int64{10, 20, 30, 40, 50, 60, 70, 80}},
		{"negatives", 2, []int64{-5, 5, -3, 3, -1, 1}},
		{"period1", 1, []int64{7, -2, 4}},
	}
	r := rand.New(rand.NewSource(42))
	for g := 0; g < 5; g++ {
		n := 20 + g*13
		v := make([]int64, n)
		for i := range v {
			v[i] = r.Int63n(200) - 100
		}
		cases = append(cases, struct {
			name   string
			period int64
			vals   []int64
		}{fmt.Sprintf("random%d", g), int64(2 + g%5), v})
	}
	return cases
}

// 不变量 1：第 m 次触发 cnt=m*period、sum=前 m*period 个元素之和。
func TestBatchRecompute(t *testing.T) {
	for _, c := range recomputeCases() {
		t.Run(c.name, func(t *testing.T) {
			q, err := New(c.period, 100000)
			if err != nil {
				t.Fatal(err)
			}
			evs := make([]Event, len(c.vals))
			for i, v := range c.vals {
				evs[i] = Event{Key: "k", Val: v}
			}
			if err := q.Feed(evs); err != nil {
				t.Fatal(err)
			}
			snaps := q.Snapshots("k")
			for i, s := range snaps {
				cnt := int64(i+1) * c.period
				var prefix int64 // 仅对每个 m 重算其前 m*p 项
				for j := int64(0); j < cnt; j++ {
					prefix += c.vals[j]
				}
				if s.Cnt != cnt || s.Sum != prefix {
					t.Fatalf("snap %d = (%d,%d), want sum %d at cnt %d", i, s.Sum, s.Cnt, prefix, cnt)
				}
			}
		})
	}
}

// 不变量 2：触发不重置——任何时刻 Totals == 已接受元素的 sum/cnt。
func TestNoReset(t *testing.T) {
	for _, c := range recomputeCases() {
		t.Run(c.name, func(t *testing.T) {
			q, _ := New(c.period, 100000)
			var sum, cnt int64
			for _, v := range c.vals { // 逐条喂，每步都核对 Totals
				if err := q.Feed([]Event{{Key: "k", Val: v}}); err != nil {
					t.Fatal(err)
				}
				sum += v
				cnt++
				gs, gc := q.Totals("k")
				if gs != sum || gc != cnt {
					t.Fatalf("after %d elems totals (%d,%d), want (%d,%d)", cnt, gs, gc, sum, cnt)
				}
			}
		})
	}
}

// 不变量 3：快照 cnt 严格递增，且每条都是截至该 cnt 的累计值。
func TestMonotonicSnapshots(t *testing.T) {
	q, _ := New(2, 1000)
	vals := []int64{3, 1, 4, 1, 5, 9, -2, 6}
	evs := make([]Event, len(vals))
	for i, v := range vals {
		evs[i] = Event{Key: "k", Val: v}
	}
	if err := q.Feed(evs); err != nil {
		t.Fatal(err)
	}
	snaps := q.Snapshots("k")
	for i, s := range snaps {
		if i > 0 && s.Cnt <= snaps[i-1].Cnt {
			t.Fatalf("cnt not strictly increasing at %d: %d <= %d", i, s.Cnt, snaps[i-1].Cnt)
		}
		var prefix int64
		for j := int64(0); j < s.Cnt; j++ {
			prefix += vals[j]
		}
		if s.Sum != prefix {
			t.Fatalf("snap %d sum %d, want cumulative %d", i, s.Sum, prefix)
		}
	}
}
