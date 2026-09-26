// Package gh 是几何哈希的几何核心：规范坐标与量化 key。
// 它不依赖本项目其他包，也不依赖第三方库。
package gh

import "math"

// Point 是整数格点。模板与场景的点均使用该类型。
type Point struct {
	X, Y int
}

// Sub 返回 p-q。
func Sub(p, q Point) Point { return Point{p.X - q.X, p.Y - q.Y} }

// Dot 为二维整数点内积。
func Dot(p, q Point) int { return p.X*q.X + p.Y*q.Y }

// Perp 返回 v 逆时针旋转 90°：Perp(x,y)=(-y,x)。
// 方向固定为逆时针，是“无反射”判定的根基：镜像副本的 v 坐标必取反。
func Perp(v Point) Point { return Point{-v.Y, v.X} }

// quantScale 是固定量化步长的倒数：步长 δ = 1/quantScale。
// 规范坐标 (u,v) 在相似变换下不变，且同副本两侧的浮点误差远小于 δ，
// 故量化到同一整数格；不取整 δ 个整数倍时不会跨格。
const quantScale = 64

// Cell 是量化后的哈希格。
type Cell struct {
	U, V int64
}

// BasisUV 返回点 p 在有序基 (a,b) 下的规范坐标：
//
//	u = <p-a, b-a> / |b-a|²
//	v = <p-a, Perp(b-a)> / |b-a|²
//
// 调用方必须保证 a≠b（|b-a|²>0）。该坐标在平移/旋转/均匀缩放
// （不含反射）下不变；反射只使 v 取反。
func BasisUV(a, b, p Point) (u, v float64) {
	d := Sub(b, a)
	r := Sub(p, a)
	denom := float64(Dot(d, d))
	u = float64(Dot(r, d)) / denom
	v = float64(Dot(r, Perp(d))) / denom
	return u, v
}

// Quantize 以固定步长 δ=1/64 四舍五入量化规范坐标。
// 必须两侧（建表与投票）使用完全一致的量化，才能保证同副本 key 相同。
func Quantize(u, v float64) Cell {
	return Cell{
		U: int64(math.Round(u * quantScale)),
		V: int64(math.Round(v * quantScale)),
	}
}

// roundAway 计算 round(x/d)（d>0），.5 远离零，与 math.Round 同规则。
func roundAway(x, d int64) int64 {
	if x >= 0 {
		return (2*x + d) / (2 * d)
	}
	return -roundAway(-x, d)
}

// CellOf 直接由整数点算出量化格号：格号 = round(64·<r,d>/|d|²)，
// 全程 int64 有理运算。同一份规范有理式（即使恰在格界 .5 上）在模板
// 与场景两侧必得到完全相同的整数格号，无浮点二义性。调用方保证 a≠b。
func CellOf(a, b, p Point) Cell {
	d := Sub(b, a)
	r := Sub(p, a)
	denom := int64(Dot(d, d))
	return Cell{
		U: roundAway(int64(Dot(r, d))*quantScale, denom),
		V: roundAway(int64(Dot(r, Perp(d)))*quantScale, denom),
	}
}

// Key 把整数点编码为 uint64。
func Key(p Point) uint64 {
	return uint64(uint32(int32(p.X)))<<32 | uint64(uint32(int32(p.Y)))
}

// Xform 施加保向相似 [c -s; s c]（行列式 c²+s²>0，不含反射）后平移。
func Xform(p []Point, c, s, tx, ty int) []Point {
	out := make([]Point, len(p))
	for i, q := range p {
		out[i] = Point{X: c*q.X - s*q.Y + tx, Y: s*q.X + c*q.Y + ty}
	}
	return out
}

// Collinear 判定点集整体共线；要求 p[0]≠p[1]（调用方已保证无重复）。
func Collinear(p []Point) bool {
	d := Sub(p[1], p[0])
	for i := 2; i < len(p); i++ {
		if r := Sub(p[i], p[0]); int64(d.X)*int64(r.Y) != int64(d.Y)*int64(r.X) {
			return false
		}
	}
	return true
}

// BruteForceContains 以定义式穷举（不走哈希/投票）判定 s 是否含 t 的保向
// 相似副本：枚举全部有序基点对确定变换，核验各点整数像在 s 中互异存在。
func BruteForceContains(t, s []Point) bool {
	at := map[uint64]int{}
	for i, p := range s {
		at[Key(p)] = i
	}
	for ti := 0; ti < len(t); ti++ {
		for tj := 0; tj < len(t); tj++ {
			if tj == ti {
				continue
			}
			uv := make([][2]float64, len(t))
			for l := range t {
				u, v := BasisUV(t[ti], t[tj], t[l])
				uv[l] = [2]float64{u, v}
			}
			for a := 0; a < len(s); a++ {
			nextB:
				for b := 0; b < len(s); b++ {
					if b == a {
						continue
					}
					dx, dy := float64(s[b].X-s[a].X), float64(s[b].Y-s[a].Y)
					used := map[int]bool{a: true, b: true}
					for l := 0; l < len(t); l++ {
						if l == ti || l == tj {
							continue // 两个基点由构造映到 s[a]、s[b]
						}
						fx := float64(s[a].X) + uv[l][0]*dx - uv[l][1]*dy
						fy := float64(s[a].Y) + uv[l][0]*dy + uv[l][1]*dx
						ix, iy := int(math.Round(fx)), int(math.Round(fy))
						if math.Abs(fx-float64(ix)) > 1e-7 || math.Abs(fy-float64(iy)) > 1e-7 {
							continue nextB
						}
						si, has := at[Key(Point{X: ix, Y: iy})]
						if !has || used[si] {
							continue nextB
						}
						used[si] = true
					}
					return true
				}
			}
		}
	}
	return false
}
