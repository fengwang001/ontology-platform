package regions

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// TestExhaustiveGrid 在小网格上与逐边叉积符号的朴素实现逐点对照，
// 覆盖各种随机的凸外环、洞、优先级与重叠关系。
func TestExhaustiveGrid(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261001, 42))
	const grid = 12

	// 随机生成严格凸多边形：取圆内的格点按极角排序（因此一定凸），
	// 再剔除共线点。范围控制在 [0,grid]。
	genRing := func(cx, cy, r int) Ring {
		pts := map[Point]bool{}
		for x := cx - r; x <= cx+r; x++ {
			for y := cy - r; y <= cy+r; y++ {
				dx := x - cx
				dy := y - cy
				if dx*dx+dy*dy <= r*r {
					pts[Point{int64(x), int64(y)}] = true
				}
			}
		}
		// 凸包（Andrew monotone chain）保证严格凸顶点集。
		var list Ring
		for p := range pts {
			list = append(list, p)
		}
		hull := convexHull(list)
		// 随机反转朝向。
		if rng.IntN(2) == 0 {
			for i, j := 0, len(hull)-1; i < j; i, j = i+1, j-1 {
				hull[i], hull[j] = hull[j], hull[i]
			}
		}
		return hull
	}

	for iter := 0; iter < 40; iter++ {
		s := NewSet()
		var regions []Region
		n := 1 + rng.IntN(4)
		for i := 0; i < n; i++ {
			r := 2 + rng.IntN(4)
			cx := r + rng.IntN(grid-2*r+1)
			cy := r + rng.IntN(grid-2*r+1)
			outer := genRing(cx, cy, r)
			reg := Region{
				ID:       fmt.Sprintf("r%d", i),
				Priority: int64(rng.IntN(5)),
				Outer:    outer,
			}
			// 约一半情况下挖一个小三角形洞。
			if rng.IntN(2) == 0 && len(outer) >= 4 {
				ix := rng.IntN(grid + 1)
				iy := rng.IntN(grid + 1)
				cand := Ring{
					{int64(ix - 1), int64(iy)},
					{int64(ix + 1), int64(iy)},
					{int64(ix), int64(iy + 1)},
				}
				if strictlyConvex(cand) && holeStrictlyInside(outer, cand) {
					reg.Hole = cand
				}
			}
			if err := s.Put(reg); err != nil {
				t.Fatalf("iter %d Put %s: %v", iter, reg.ID, err)
			}
			regions = append(regions, reg)
		}

		// 全点对照：集合命中集合 == 朴素实现命中集合，顺序也按 (优先级降序, id 升序)。
		for x := 0; x <= grid; x++ {
			for y := 0; y <= grid; y++ {
				got, err := s.Locate(int64(x), int64(y))
				if err != nil {
					t.Fatalf("Locate(%d,%d): %v", x, y, err)
				}
				var wantIDs []string
				for _, r := range regions {
					if naiveContains(r, x, y) {
						wantIDs = append(wantIDs, r.ID)
					}
				}
				sortNaive(regions, &wantIDs)
				gotIDs := ids(got)
				if len(wantIDs) == 0 {
					wantIDs = []string{}
				}
				if !eqStrings(gotIDs, wantIDs) {
					t.Fatalf("iter %d (%d,%d): naive=%v impl=%v", iter, x, y, wantIDs, gotIDs)
				}
			}
		}
		t.Logf("输入: 第 %d 轮随机区域 %d 个（含洞 %d 个）-> 输出: 网格 [%d,%d]^2 全部 %d 点与朴素实现一致",
			iter, len(regions), countHoles(regions), 0, grid, (grid+1)*(grid+1))
	}
}

func sortNaive(regions []Region, ids *[]string) {
	prio := map[string]int64{}
	for _, r := range regions {
		prio[r.ID] = r.Priority
	}
	// 简单插入排序，保持测试朴素。
	s := *ids
	for i := 1; i < len(s); i++ {
		for j := i; j > 0; j-- {
			if prio[s[j]] > prio[s[j-1]] || (prio[s[j]] == prio[s[j-1]] && s[j] < s[j-1]) {
				s[j-1], s[j] = s[j], s[j-1]
			} else {
				break
			}
		}
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countHoles(rs []Region) int {
	n := 0
	for _, r := range rs {
		if len(r.Hole) > 0 {
			n++
		}
	}
	return n
}

func convexHull(pts Ring) Ring {
	if len(pts) <= 1 {
		return pts
	}
	// 按 (x,y) 排序
	for i := 1; i < len(pts); i++ {
		for j := i; j > 0; j-- {
			if pts[j].X < pts[j-1].X || (pts[j].X == pts[j-1].X && pts[j].Y < pts[j-1].Y) {
				pts[j-1], pts[j] = pts[j], pts[j-1]
			} else {
				break
			}
		}
	}
	cross3 := func(a, b, c Point) int64 {
		return (b.X-a.X)*(c.Y-b.Y) - (b.Y-a.Y)*(c.X-b.X)
	}
	var lower, upper Ring
	for _, p := range pts {
		for len(lower) >= 2 && cross3(lower[len(lower)-2], lower[len(lower)-1], p) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, p)
	}
	for i := len(pts) - 1; i >= 0; i-- {
		p := pts[i]
		for len(upper) >= 2 && cross3(upper[len(upper)-2], upper[len(upper)-1], p) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, p)
	}
	return append(lower[:len(lower)-1], upper[:len(upper)-1]...)
}
