// Package rc 用旋转卡壳在 O(n) 内枚举凸多边形全部对跖点对并求直径。依赖 cal。
package rc

import (
	"errors"
	"math"
	"sort"

	"ontology/cal"
)

// Diameter 返回凸多边形（顶点逆时针）直径的最大平方距离与全部并列的顶点
// 索引对（每对 i<j，整体按下标排序，结果确定）。输入合法性由上层保证。
// 只读入参，可并发调用。
func Diameter(poly []cal.Point) (int64, [][2]int) {
	s := &solver{poly: poly}
	return s.solve()
}

// solver 是单次求解的状态；cnt 是非导出计数器，记录实际求过 d² 的顶点对
// 个数，仅供包内自检与包内测试读取，不出现在公开接口。
type solver struct {
	poly []cal.Point
	cnt  int
}

func (s *solver) solve() (int64, [][2]int) {
	p := s.poly
	n := len(p)
	var best int64
	seen := map[[2]int]bool{}
	consider := func(i, j int) {
		if i == j {
			return
		}
		d := cal.Dist2(p[i], p[j])
		s.cnt++
		if d < best {
			return
		}
		if d > best {
			best, seen = d, map[[2]int]bool{}
		}
		if i > j {
			i, j = j, i
		}
		seen[[2]int{i, j}] = true
	}
	j := 1
	for i := 0; i < n; i++ {
		ni := (i + 1) % n
		for cal.Area2(p[i], p[ni], p[(j+1)%n]) > cal.Area2(p[i], p[ni], p[j]) {
			j = (j + 1) % n
		}
		consider(i, j)
		consider(ni, j)
	}
	pairs := make([][2]int, 0, len(seen))
	for pr := range seen {
		pairs = append(pairs, pr)
	}
	sort.Slice(pairs, func(a, b int) bool {
		return pairs[a][0] < pairs[b][0] || pairs[a][0] == pairs[b][0] && pairs[a][1] < pairs[b][1]
	})
	return best, pairs
}

// SelfCheck 在包内核验计数器随顶点数线性增长（cnt <= 3m，远小于 m²），
// 只暴露通过/失败，不暴露计数器数值。
func SelfCheck() error {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		s := &solver{poly: regular(m)}
		s.solve()
		if s.cnt > 3*m {
			return errors.New("rc: pair counter grows super-linearly")
		}
	}
	return nil
}

// regular 生成近似正 m 边形的整数点凸多边形（供自检与测试）。
func regular(m int) []cal.Point {
	p := make([]cal.Point, m)
	for i := range p {
		// 顶点均匀分布在半径 9000 的圆上，取整。
		th := 2 * math.Pi * float64(i) / float64(m)
		p[i] = cal.Point{X: int64(9000*math.Cos(th) + 0.5), Y: int64(9000*math.Sin(th) + 0.5)}
	}
	return p
}
