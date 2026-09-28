package txnset

import "slices"

// Interval 是一个事务号闭区间 [Lo, Hi]，不变式为 0 <= Lo <= Hi。
type Interval struct {
	Lo int64
	Hi int64
}

// normalizeIntervals 对同一来源的区间按 Lo 排序，并合并重叠或相邻
// （Hi+1 == 下一个 Lo）的区间。返回新切片，不修改入参。
// 输入区间必须满足 Lo <= Hi。
func normalizeIntervals(in []Interval) []Interval {
	if len(in) == 0 {
		return nil
	}
	out := make([]Interval, len(in))
	copy(out, in)
	slices.SortFunc(out, func(a, b Interval) int {
		if a.Lo < b.Lo {
			return -1
		}
		if a.Lo > b.Lo {
			return 1
		}
		return 0
	})

	merged := out[:1]
	for _, iv := range out[1:] {
		last := &merged[len(merged)-1]
		// 重叠（iv.Lo <= last.Hi）或相邻（iv.Lo == last.Hi+1）即合并。
		// 用 iv.Lo-1 <= last.Hi 表达，避免 last.Hi == MaxInt64 时加一溢出。
		if iv.Lo == 0 || iv.Lo-1 <= last.Hi {
			if iv.Hi > last.Hi {
				last.Hi = iv.Hi
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}

// subtractIntervals 计算闭区间集合 a - b。
// a、b 均须已规范化（升序、互不重叠、互不相邻）；返回值同样已规范化。
func subtractIntervals(a, b []Interval) []Interval {
	var out []Interval
	j := 0
	for _, x := range a {
		lo, hi := x.Lo, x.Hi
		// b 升序：跳过完全位于当前 a 区间左侧的区间；
		// 后续 a 区间起点更大，j 在循环间保持安全。
		for j < len(b) && b[j].Hi < lo {
			j++
		}
		k := j
		exhausted := false
		for k < len(b) && b[k].Lo <= hi {
			cut := b[k]
			if cut.Lo > lo {
				// 保留 [lo, cut.Lo-1] 这一段（cut.Lo>lo>=0，故 cut.Lo-1 安全）
				out = append(out, Interval{Lo: lo, Hi: cut.Lo - 1})
			}
			if cut.Hi >= hi {
				// 切除区间已覆盖当前 a 区间末端；直接结束，避免 Hi+1 溢出。
				exhausted = true
				break
			}
			lo = cut.Hi + 1
			k++
		}
		if !exhausted && lo <= hi {
			out = append(out, Interval{Lo: lo, Hi: hi})
		}
	}
	return out
}
