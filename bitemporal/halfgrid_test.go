package bitemporal

import (
	"math/rand"
	"sort"
	"testing"
)

// TestHalfGridExhaustive 对单个半事实流做全网格真值对照：
// 朴素逐事实扫描 vs 网格矩形穿刺。
func TestHalfGridExhaustive(t *testing.T) {
	cases := [][]linkFact{
		{
			{src: "a", dst: "b", kind: kindCreate, validTime: 1, recordTime: 1},
			{src: "a", dst: "b", kind: kindCreate, validTime: 3, recordTime: 6},
			{src: "a", dst: "b", kind: kindRevoke, validTime: 5, recordTime: 9},
			{src: "a", dst: "b", kind: kindCreate, validTime: 6, recordTime: 10},
		},
		{
			{src: "a", dst: "b", kind: kindCreate, validTime: 2, recordTime: 8},
			{src: "a", dst: "b", kind: kindRevoke, validTime: 2, recordTime: 3},
			{src: "a", dst: "b", kind: kindCreate, validTime: 5, recordTime: 4},
		},
	}
	for ci, fs := range cases {
		hs := buildHalfStream(fs)
		rects := halfRects(hs, 7)
		// 收集全部 rt/vt 断点。
		ticks := map[int64]struct{}{}
		for _, f := range fs {
			ticks[f.recordTime] = struct{}{}
			ticks[f.validTime] = struct{}{}
		}
		for _, r := range rects {
			ticks[r.rt0] = struct{}{}
			ticks[r.rt1] = struct{}{}
			ticks[r.vt0] = struct{}{}
			ticks[r.vt1] = struct{}{}
		}
		var ts []int64
		for x := range ticks {
			if x != vtInf {
				ts = append(ts, x)
			}
		}
		ts = append(ts, 0)
		for _, R := range ts {
			for _, V := range ts {
				got := rectAlive(rects, R, V)
				want := naiveHalfAlive(fs, R, V)
				if got != want {
					t.Fatalf("case=%d R=%d V=%d rect=%v naive=%v rects=%+v",
						ci, R, V, got, want, rects)
				}
			}
		}
	}
}

func rectAlive(rects []rect, R, V int64) bool {
	alive := false
	for _, r := range rects {
		if R >= r.rt0 && R < r.rt1 && V >= r.vt0 && V < r.vt1 {
			alive = r.alive
		}
	}
	return alive
}

func naiveHalfAlive(fs []linkFact, R, V int64) bool {
	// 直接取全顺序最大者。
	best := -1
	for i, f := range fs {
		if f.recordTime <= R && f.validTime <= V {
			if best == -1 || orderLess(fs[best], f) {
				best = i
			}
		}
	}
	if best == -1 {
		return false
	}
	return fs[best].kind == kindCreate
}

func orderLess(a, b linkFact) bool {
	if a.validTime != b.validTime {
		return a.validTime < b.validTime
	}
	if a.recordTime != b.recordTime {
		return a.recordTime < b.recordTime
	}
	return a.kind < b.kind
}

func TestHalfGridRandom(t *testing.T) {
	for seed := int64(0); seed < 3000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(10)
		var fs []linkFact
		for i := 0; i < n; i++ {
			fs = append(fs, linkFact{
				src: "a", dst: "b",
				kind:       []int{kindCreate, kindRevoke}[rng.Intn(2)],
				validTime:  int64(rng.Intn(10)),
				recordTime: int64(rng.Intn(10)),
			})
		}
		sort.SliceStable(fs, func(i, j int) bool { return factLess(fs[i], fs[j]) })
		hs := buildHalfStream(fs)
		rects := halfRects(hs, 1)
		for R := int64(0); R <= 11; R++ {
			for V := int64(0); V <= 11; V++ {
				if rectAlive(rects, R, V) != naiveHalfAlive(fs, R, V) {
					t.Fatalf("seed=%d R=%d V=%d\nfs=%+v\nrects=%+v",
						seed, R, V, fs, rects)
				}
			}
		}
	}
}

func TestZZSuffix(t *testing.T) {
	fs := []linkFact{
		{src: "a", dst: "b", kind: kindRevoke, validTime: 0, recordTime: 0},
		{src: "a", dst: "b", kind: kindCreate, validTime: 0, recordTime: 6},
		{src: "a", dst: "b", kind: kindCreate, validTime: 1, recordTime: 2},
	}
	hs := buildHalfStream(fs)
	t.Logf("vt=%v rt=%v kind=%v suffix=%v first=%v", hs.vt, hs.rt, hs.kind, hs.suffixMinRT, hs.first)
}

func TestZZSegs(t *testing.T) {
	fs := []linkFact{
		{src: "a", dst: "b", kind: kindRevoke, validTime: 0, recordTime: 0},
		{src: "a", dst: "b", kind: kindCreate, validTime: 0, recordTime: 6},
		{src: "a", dst: "b", kind: kindCreate, validTime: 1, recordTime: 2},
	}
	hs := buildHalfStream(fs)
	t.Logf("rects0=%+v", halfRects(hs, 1))
}
