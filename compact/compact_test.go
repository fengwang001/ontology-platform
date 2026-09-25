package compact

import (
	"fmt"
	"testing"

	"ontology/rec"
)

// TestCompareCountBounded 证明按 Key 哈希定位最新记录：
// 先 Feed m 条互异 Key，再 Feed 一条已出现 Key 的新记录，
// 覆盖全部的 Compact 中该 Key 的折叠比较条数是不随 m 增长的小常数。
func TestCompareCountBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			var e Engine
			for i := 0; i < m; i++ {
				e.Feed(rec.Rec{Key: fmt.Sprintf("k%06d", i), Value: i, TS: int64(i)})
			}
			// 已出现过的 Key 再来一条更晚的记录
			e.Feed(rec.Rec{Key: "k000000", Value: -1, TS: int64(m)})
			out := e.Compact(0, int64(m)+1, 0)
			if len(out) != m {
				t.Fatalf("got %d records, want %d", len(out), m)
			}
			if out[0].Value != -1 {
				t.Fatalf("k000000 not folded to latest: %+v", out[0])
			}
			// 只有 k000000 发生一次折叠比较；与 m 无关的小常数
			if got := e.cmps.Load(); got > 1 {
				t.Fatalf("comparisons %d grow with m=%d, want <= 1", got, m)
			}
		})
	}
}

// TestFoldLatestPerKey 表驱动：同 Key 多条记录只留 TS 最大者，与到达顺序无关。
func TestFoldLatestPerKey(t *testing.T) {
	cases := []struct {
		name string
		in   []rec.Rec
		want rec.Rec
	}{
		{"升序到达", []rec.Rec{{Key: "a", Value: 1, TS: 1}, {Key: "a", Value: 2, TS: 4}}, rec.Rec{Key: "a", Value: 2, TS: 4}},
		{"降序到达", []rec.Rec{{Key: "d", Value: 7, TS: 6}, {Key: "d", Value: 9, TS: 2}}, rec.Rec{Key: "d", Value: 7, TS: 6}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e Engine
			for _, r := range c.in {
				e.Feed(r)
			}
			out := e.Compact(0, 100, 0)
			if len(out) != 1 || out[0] != c.want {
				t.Fatalf("got %v, want [%v]", out, c.want)
			}
		})
	}
}
