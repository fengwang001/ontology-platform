// Package hp 提供半平面谓词与直线/线段求交。
// 半平面 h: A*x + B*y <= C；Side<=0 表示点在内（含边界）。
// 全部坐标使用 math/big.Rat 精确表示，避免浮点把边界点判错。
package hp

import "math/big"

// Point 是平面上一个精确有理点。
type Point struct {
	X, Y *big.Rat
}

// HalfPlane 表示整数半平面 A*x + B*y <= C，(A,B) 不全为零。
type HalfPlane struct {
	A, B, C int64
}

// Pt 用整数坐标构造点。
func Pt(x, y int64) Point {
	return Point{X: big.NewRat(x, 1), Y: big.NewRat(y, 1)}
}

// Rat 把 int64 包装成 *big.Rat。
func Rat(n int64) *big.Rat { return big.NewRat(n, 1) }

// Side 返回 A*x + B*y - C：<=0 在内，==0 在边界上，>0 在外。
func Side(h HalfPlane, p Point) *big.Rat {
	s := new(big.Rat).Mul(big.NewRat(h.A, 1), p.X)
	s.Add(s, new(big.Rat).Mul(big.NewRat(h.B, 1), p.Y))
	return s.Sub(s, big.NewRat(h.C, 1))
}

// Intersect 求边界直线 h 与有向线段 p->q 的交点。
// 直线与线段平行（无唯一交点）时 ok=false。
func Intersect(h HalfPlane, p, q Point) (z Point, ok bool) {
	sp, sq := Side(h, p), Side(h, q)
	den := new(big.Rat).Sub(sp, sq)
	if den.Sign() == 0 {
		return Point{}, false
	}
	// z = p + t*(q-p)，取 t = sp/(sp-sq) 使 Side(h,z)=0。
	t := new(big.Rat).Quo(sp, den)
	z.X = new(big.Rat).Add(p.X, new(big.Rat).Mul(t, new(big.Rat).Sub(q.X, p.X)))
	z.Y = new(big.Rat).Add(p.Y, new(big.Rat).Mul(t, new(big.Rat).Sub(q.Y, p.Y)))
	return z, true
}

// Eq 精确判等（big.Rat 已约分，Cmp 即数值相等）。
func Eq(p, q Point) bool { return p.X.Cmp(q.X) == 0 && p.Y.Cmp(q.Y) == 0 }
