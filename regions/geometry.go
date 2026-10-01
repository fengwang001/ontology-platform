package regions

import "math/big"

const maxCoord = 1_000_000_000

// Point 是一个整数坐标点。
type Point struct {
	X int64
	Y int64
}

// Ring 是一个严格凸多边形，顶点按顺时针或逆时针顺序给出。
type Ring []Point

// Region 是一个带可选内部禁区（洞）的凸多边形区域。
type Region struct {
	ID       string
	Priority int64
	Outer    Ring
	Hole     Ring // 可为空
}

func coordsInRange(pts []Point) bool {
	for _, p := range pts {
		if p.X < -maxCoord || p.X > maxCoord || p.Y < -maxCoord || p.Y > maxCoord {
			return false
		}
	}
	return true
}

// cross 返回向量 b-a 与 c-b 的叉积（z 分量），用 big.Int 避免溢出。
func cross(a, b, c Point) *big.Int {
	var z big.Int
	z.Mul(big.NewInt(b.X-a.X), big.NewInt(c.Y-b.Y))
	z.Sub(&z, new(big.Int).Mul(big.NewInt(b.Y-a.Y), big.NewInt(c.X-b.X)))
	return &z
}

func crossAt(r Ring, i int) *big.Int {
	n := len(r)
	return cross(r[(i-1+n)%n], r[i], r[(i+1)%n])
}

// strictlyConvex 判断环是否为严格凸多边形：
//  1. 每个顶点处相邻三边转向同号且非零（不存在共线/折返）；
//  2. 对每条有向边，其余所有顶点都严格位于内侧半平面，
//     由此排除五角星之类每个顶点同号转向、但整圈绕多周的自交序列。
func strictlyConvex(r Ring) bool {
	n := len(r)
	if n < 3 {
		return false
	}
	sign := 0
	for i := range r {
		z := crossAt(r, i)
		s := z.Sign()
		if s == 0 {
			return false
		}
		if sign == 0 {
			sign = s
		} else if s != sign {
			return false
		}
	}
	for i := range r {
		a := r[i]
		b := r[(i+1)%n]
		for j := range r {
			if j == i || j == (i+1)%n {
				continue
			}
			if cross(a, b, r[j]).Sign() != sign {
				return false
			}
		}
	}
	return true
}

// ringContainsClosed 判断点是否位于环的闭区域内（边与顶点算内）。
func ringContainsClosed(r Ring, p Point) bool {
	n := len(r)
	sign := crossAt(r, 0).Sign()
	for i := range r {
		if cross(r[i], r[(i+1)%n], p).Sign()*sign < 0 {
			return false
		}
	}
	return true
}

// ringContainsOpen 判断点是否严格位于环内部（不在边上）。
func ringContainsOpen(r Ring, p Point) bool {
	n := len(r)
	sign := crossAt(r, 0).Sign()
	for i := range r {
		if cross(r[i], r[(i+1)%n], p).Sign()*sign <= 0 {
			return false
		}
	}
	return true
}

// holeStrictlyInside 要求洞的每个顶点都严格位于外环内部（不在边上）。
func holeStrictlyInside(outer, hole Ring) bool {
	for _, p := range hole {
		if !ringContainsOpen(outer, p) {
			return false
		}
	}
	return true
}
