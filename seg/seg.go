// Package seg 提供区间的边界与几何判定：重叠/邻接/接触、并集合并、
// 撤回拆分。区间一律左闭右开 [S,E)，要求 S < E。本包不依赖其他包。
package seg

import "sort"

// Interval 是一段左闭右开区间 [S,E)。
type Interval struct {
	S, E int64
}

// Valid 报告 s < e 是否成立（合法区间的必要条件）。
func Valid(s, e int64) bool { return s < e }

// Touches 报告区间 i 与 [s,e) 是否接触：重叠或邻接都算接触。
// 用于 Add 的归并判定。
func Touches(i Interval, s, e int64) bool { return i.E >= s && i.S <= e }

// Overlaps 报告区间 i 与 [s,e) 是否严格重叠（贴边不算）。
// 用于 Withdraw 的命中判定。
func Overlaps(i Interval, s, e int64) bool { return i.S < e && s < i.E }

// Merge 返回 i 与 [s,e) 合并后的区间。调用前需保证二者接触。
func Merge(i Interval, s, e int64) Interval {
	if i.S < s {
		s = i.S
	}
	if i.E > e {
		e = i.E
	}
	return Interval{S: s, E: e}
}

// Maximal 报告有序分段是否「归并完备」：相邻段满足 next.S > prev.E，
// 即既无重叠也无邻接，每段都是最大的。
func Maximal(v []Interval) bool {
	for i := 1; i < len(v); i++ {
		if v[i].S <= v[i-1].E {
			return false
		}
	}
	return true
}

// NaiveAdd 朴素并集合并：把 [s,e) 并入有序分段，整体重算，O(n log n)。
// 调用方需保证 [s,e) 合法；返回新的最大不相交分段。
func NaiveAdd(segs []Interval, s, e int64) []Interval {
	segs = append(segs, Interval{S: s, E: e})
	sort.Slice(segs, func(i, j int) bool { return segs[i].S < segs[j].S })
	m := segs[:1]
	for _, v := range segs[1:] {
		if last := m[len(m)-1]; Touches(last, v.S, v.E) {
			m[len(m)-1] = Merge(last, v.S, v.E)
		} else {
			m = append(m, v)
		}
	}
	return m
}

// NaiveWithdraw 朴素撤回拆分：从有序分段撤掉 [s,e)，整体重算，O(n)。
// 与所有段都无严格重叠时原样返回（是否报错由调用方判定）。
func NaiveWithdraw(segs []Interval, s, e int64) []Interval {
	out := make([]Interval, 0, len(segs)+1) // 拆分会变长，不能原地复用
	for _, v := range segs {
		if !Overlaps(v, s, e) {
			out = append(out, v)
			continue
		}
		l, r, hl, hr := Split(v, s, e)
		if hl {
			out = append(out, l)
		}
		if hr {
			out = append(out, r)
		}
	}
	return out
}

// Split 从区间 i 中撤掉 [s,e)，返回左右残留段及各自是否非空。
// 调用前需保证二者严格重叠。残留段可能为空（此时 has 为 false），
// 调用方不得输出空段。
func Split(i Interval, s, e int64) (left, right Interval, hasLeft, hasRight bool) {
	if i.S < s { // 严格重叠保证 s < i.E，故 [i.S,s) 非空
		left, hasLeft = Interval{S: i.S, E: s}, true
	}
	if e < i.E { // 严格重叠保证 i.S < e，故 [e,i.E) 非空
		right, hasRight = Interval{S: e, E: i.E}, true
	}
	return left, right, hasLeft, hasRight
}
