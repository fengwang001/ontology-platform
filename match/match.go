// Package match 在 gh 之上实现几何哈希：模板量化表、场景投票、全局最大基。只依赖 gh。
package match

import (
	"math"
	"math/rand/v2"

	"ontology/gh"
)

// Template 校验通过后不可变。基 (i0,i1) 取最远点对（直径），故 |u|,|v|≤1；
// 浮点只生成候选整数点，最终用 int64 有理等式精确核验。
type Template struct {
	pts    []gh.Point
	i0, i1 int
	u, v   []float64 // 规范坐标，基点为 (0,0)、(1,0)
	rU, rV []int64   // 分子 <r,d>、<r,Perp(d)>；denom=|d|²
	denom  int64
	table  map[gh.Cell][]int
}

// NewTemplate 假定 pts 已由上层校验（≥3、无重复、不越界、非共线）。
func NewTemplate(pts []gh.Point) *Template {
	t := &Template{pts: pts, u: make([]float64, len(pts)), v: make([]float64, len(pts)),
		rU: make([]int64, len(pts)), rV: make([]int64, len(pts)), table: map[gh.Cell][]int{}}
	for i := 0; i < len(pts); i++ { // 最远点对作基
		for j := i + 1; j < len(pts); j++ {
			e := gh.Sub(pts[j], pts[i])
			if d2 := int64(gh.Dot(e, e)); d2 > t.denom {
				t.denom, t.i0, t.i1 = d2, i, j
			}
		}
	}
	a, b, d := pts[t.i0], pts[t.i1], gh.Sub(pts[t.i1], pts[t.i0])
	for l := range pts {
		t.u[l], t.v[l] = gh.BasisUV(a, b, pts[l])
		t.rU[l] = int64(gh.Dot(gh.Sub(pts[l], a), d))
		t.rV[l] = int64(gh.Dot(gh.Sub(pts[l], a), gh.Perp(d)))
		if l != t.i0 && l != t.i1 { // 整数有理格号建表，基点不入表
			c := gh.CellOf(a, b, pts[l])
			t.table[c] = append(t.table[c], l)
		}
	}
	return t
}

// K 返回模板点数。
func (t *Template) K() int { return len(t.pts) }

// Result.votes 是非导出计数器：一次 Match 的投票数 n(n-1)(k-2)，不经公开接口暴露。
type Result struct {
	Found   bool
	Mapping []int // Mapping[模板点索引] = 场景点索引
	votes   int64
}

const predEps = 1e-7

// Match 对场景每个有序基把模板 k-2 个非基点反算查表投票，全局最大基支持数
// 达 k 判匹配（同票取序最小基）。基有序、Perp 逆时针：镜像 v 取反核验失败。
func (t *Template) Match(scene []gh.Point) *Result {
	n, k := len(scene), len(t.pts)
	r := &Result{}
	at := make(map[uint64]int, n)
	for i, p := range scene {
		at[pointKey(p.X, p.Y)] = i
	}
	hit := make([]int, k)
	best := -1
	for a := 0; a < n; a++ {
		for b := 0; b < n; b++ {
			if b == a {
				continue
			}
			ax, ay := float64(scene[a].X), float64(scene[a].Y)
			dx, dy := float64(scene[b].X-scene[a].X), float64(scene[b].Y-scene[a].Y)
			sdx, sdy := int64(scene[b].X-scene[a].X), int64(scene[b].Y-scene[a].Y)
			sup := 2
			for l := 0; l < k; l++ {
				if l == t.i0 || l == t.i1 {
					continue
				}
				r.votes++ // 一次「点-基坐标投票」
				fx := ax + t.u[l]*dx - t.v[l]*dy
				fy := ay + t.u[l]*dy + t.v[l]*dx
				ix, iy := int(math.Round(fx)), int(math.Round(fy))
				if math.Abs(fx-float64(ix)) > predEps || math.Abs(fy-float64(iy)) > predEps {
					continue // 非整数预测点：该基下无此点
				}
				si, ok := at[pointKey(ix, iy)]
				if !ok || si == a || si == b {
					continue
				}
				inCell := false // 量化哈希连接
				for _, m := range t.table[gh.CellOf(scene[a], scene[b], scene[si])] {
					if m == l {
						inCell = true
					}
				}
				if !inCell {
					continue
				}
				// 精确核验（乘模板 denom）：q·denom = rU·d ± rV·Perp(d)；
				// 镜像 rV 带号不符必失败；坐标界内 int64 不溢出。
				qx, qy := int64(ix-scene[a].X), int64(iy-scene[a].Y)
				if qx*t.denom != t.rU[l]*sdx-t.rV[l]*sdy ||
					qy*t.denom != t.rU[l]*sdy+t.rV[l]*sdx {
					continue
				}
				hit[l], sup = si, sup+1
			}
			if sup > best { // 全局最大基；严格大于 => 同票保留序最小基
				best = sup
				if sup == k {
					r.Found = true
					r.Mapping = make([]int, k)
					r.Mapping[t.i0], r.Mapping[t.i1] = a, b
					for l := 0; l < k; l++ {
						if l != t.i0 && l != t.i1 {
							r.Mapping[l] = hit[l]
						}
					}
				}
			}
		}
	}
	return r
}

func pointKey(x, y int) uint64 { return uint64(uint32(int32(x)))<<32 | uint64(uint32(int32(y))) }

// QuadraticVoteCountVerified 核验票数随 n² 增长，只回布尔，不泄露计数器数值。
func QuadraticVoteCountVerified() bool {
	t := NewTemplate([]gh.Point{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 8}})
	rg := rand.New(rand.NewPCG(776, 0x9e3779b97f4a7c15))
	for _, n := range []int{100, 1000, 10000} {
		seen, s := map[uint64]bool{}, make([]gh.Point, 0, n)
		for len(s) < n {
			p := gh.Point{X: int(rg.Int64()) % 9001, Y: int(rg.Int64()) % 9001}
			if k := pointKey(p.X, p.Y); !seen[k] {
				seen[k], s = true, append(s, p)
			}
		}
		if r := t.Match(s); r.votes != int64(n)*int64(n-1)*int64(t.K()-2) {
			return false // 必为 n(n-1)(k-2)：Θ(n²) 且 ≤(k-2)n²
		}
	}
	return true
}
