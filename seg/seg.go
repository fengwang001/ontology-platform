// Package seg 提供区间的边界与几何判定：重叠/邻接/接触、并集合并、撤回拆分。
// 区间一律左闭右开 [S,E)，要求 S < E。本包不依赖工程内其他包。
package seg

// Seg 是一段左闭右开区间 [S,E)。
type Seg struct{ S, E int64 }

// New 构造区间；s >= e 时 ok=false。
func New(s, e int64) (seg Seg, ok bool) {
	if s >= e {
		return Seg{}, false
	}
	return Seg{s, e}, true
}

// Overlaps 判定严格重叠：a.S < b.E 且 b.S < a.E（贴边不算）。
func Overlaps(a, b Seg) bool { return a.S < b.E && b.S < a.E }

// Adjacent 判定邻接：端点恰好相接但无重叠。
func Adjacent(a, b Seg) bool { return a.E == b.S || b.E == a.S }

// Touches 判定接触：重叠或邻接都算，即 a.E >= b.S 且 a.S <= b.E。
func Touches(a, b Seg) bool { return a.E >= b.S && a.S <= b.E }

// Merge 返回两段接触区间的并 [min(S), max(E))；调用方保证两段接触。
func Merge(a, b Seg) Seg {
	if b.S < a.S {
		a.S = b.S
	}
	if b.E > a.E {
		a.E = b.E
	}
	return a
}

// Split 从 i 中撤掉 [s,e)（调用方保证严格重叠），返回左右残留段。
// 左段 [i.S, min(s,i.E)) 仅当 i.S < s；右段 [max(e,i.S), i.E) 仅当 e < i.E。
// 空段不返回（hasLeft/hasRight 为 false）。
func Split(i Seg, s, e int64) (left, right Seg, hasLeft, hasRight bool) {
	if i.S < s {
		left, hasLeft = Seg{i.S, min(s, i.E)}, true
	}
	if e < i.E {
		right, hasRight = Seg{max(e, i.S), i.E}, true
	}
	return left, right, hasLeft, hasRight
}
