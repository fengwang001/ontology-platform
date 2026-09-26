package match

import (
	"math/rand/v2"
	"testing"

	"ontology/gh"
)

// TestCounterQuadratic 钉住复杂度：固定 k 的模板，票数必须恰为
// n(n-1)(k-2)，且恒 ≤ c·n²（c=k-2 为小常数），n 取 100..10000 多档。
// 计数器是非导出字段 r.votes，测试在包内直接读取（不经任何公开接口）。
func TestCounterQuadratic(t *testing.T) {
	tpl := NewTemplate([]gh.Point{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 8}}) // k=3
	c := int64(tpl.K() - 2)
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		r := tpl.Match(randSet(rand.New(rand.NewPCG(uint64(n), 91)), n))
		want := int64(n) * int64(n-1) * c
		if r.votes != want {
			t.Fatalf("n=%d votes=%d want=%d", n, r.votes, want)
		}
		if r.votes > c*int64(n)*int64(n) { // Θ(n²) 上界，小常数 c
			t.Fatalf("n=%d votes=%d exceed c*n^2=%d", n, r.votes, c*int64(n)*int64(n))
		}
	}
}

// randSet 确定性生成互不重复的随机整数点（坐标 ±900）。
func randSet(rg *rand.Rand, n int) []gh.Point {
	seen, out := map[uint64]bool{}, make([]gh.Point, 0, n)
	for len(out) < n {
		p := gh.Point{X: int(rg.Int64()) % 900, Y: int(rg.Int64()) % 900}
		if !seen[gh.Key(p)] {
			seen[gh.Key(p)], out = true, append(out, p)
		}
	}
	return out
}

// nonCollinear 取随机点集的前 k 个非共线点，构造合法模板。
func nonCollinear(rg *rand.Rand, k int) []gh.Point {
	pool := randSet(rg, 40)
	for i := 0; i+k <= len(pool); i++ {
		if !gh.Collinear(pool[i : i+k]) {
			return pool[i : i+k]
		}
	}
	return pool[:k]
}

// TestExhaustiveConsistency 与定义式穷举逐条对齐：哈希 found 必须等于
// gh.BruteForceContains；found=true 时映射必须是互异场景点且满足精确核验。
func TestExhaustiveConsistency(tb *testing.T) {
	rg := rand.New(rand.NewPCG(776, 20260926))
	mats := [][2]int{{1, 0}, {0, 1}, {2, 1}, {1, 2}, {-1, 2}, {3, -1}}
	for iter := 0; iter < 200; iter++ {
		k := 3 + int(rg.Int64())%3 // k ∈ {3,4,5}
		tplPts := nonCollinear(rg, k)
		tpl := NewTemplate(tplPts)
		var sce []gh.Point
		if iter%2 == 0 { // 一半埋入真实保向相似副本
			c := mats[int(rg.Int64())%len(mats)]
			sce = gh.Xform(tplPts, c[0], c[1], 1000+int(rg.Int64())%500, 1000+int(rg.Int64())%500)
		}
		sce = append(sce, randSet(rg, 4+int(rg.Int64())%5)...)
		r := tpl.Match(sce)
		brute := gh.BruteForceContains(tplPts, sce)
		if r.Found != brute {
			tb.Fatalf("iter=%d k=%d hash=%v brute=%v\n tpl=%v\n sce=%v",
				iter, k, r.Found, brute, tplPts, sce)
		}
		if r.Found && !validMapping(tpl, tplPts, sce, r.Mapping) {
			tb.Fatalf("iter=%d invalid mapping %v", iter, r.Mapping)
		}
	}
}

// validMapping 独立复核映射：索引互异、点存在，且在模板最远基下满足
// 与 match 相同的 int64 有理相似等式（保向）。
func validMapping(t *Template, tp, sce []gh.Point, m []int) bool {
	if len(m) != len(tp) {
		return false
	}
	used := map[int]bool{}
	for _, si := range m {
		if si < 0 || si >= len(sce) || used[si] {
			return false
		}
		used[si] = true
	}
	a, b := sce[m[t.i0]], sce[m[t.i1]]
	sdx, sdy := int64(b.X-a.X), int64(b.Y-a.Y)
	for l := range tp {
		if l == t.i0 || l == t.i1 {
			continue
		}
		q := sce[m[l]]
		qx, qy := int64(q.X-a.X), int64(q.Y-a.Y)
		if qx*t.denom != t.rU[l]*sdx-t.rV[l]*sdy ||
			qy*t.denom != t.rU[l]*sdy+t.rV[l]*sdx {
			return false
		}
	}
	return true
}
