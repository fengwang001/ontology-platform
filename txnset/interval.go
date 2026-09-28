package txnset

import "sort"

// Interval 表示某来源下的一个闭区间事务号集合 [Lo, Hi]。
// Lo == Hi 时即单点。
type Interval struct {
	Lo uint64
	Hi uint64
}

// mergeIntervals 接收任意顺序、可重叠、可相邻的区间，
// 返回按起点升序、合并重叠与相邻区间后的规范切片。
// 入参不会被修改；输入为空时返回 nil。
func mergeIntervals(in []Interval) []Interval {
	if len(in) == 0 {
		return nil
	}
	out := make([]Interval, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lo != out[j].Lo {
			return out[i].Lo < out[j].Lo
		}
		return out[i].Hi < out[j].Hi
	})

	merged := make([]Interval, 0, len(out))
	cur := out[0]
	for _, iv := range out[1:] {
		switch {
		case iv.Lo <= cur.Hi:
			// 真重叠（含完全包含）：扩展右端点。
			if iv.Hi > cur.Hi {
				cur.Hi = iv.Hi
			}
		case iv.Lo == cur.Hi+1:
			// 相邻（闭区间语义下 [1,2] 与 [3,4] 无空洞）：合并。
			// cur.Hi 为 math.MaxUint64 时 iv.Lo 不可能等于 cur.Hi+1（回绕为 0）。
			cur.Hi = iv.Hi
		default:
			merged = append(merged, cur)
			cur = iv
		}
	}
	merged = append(merged, cur)
	return merged
}

// subtractIntervals 计算闭区间集合 a - b（a、b 均须为规范切片），
// 返回规范切片。入参不会被修改。
// 采用双指针推进：对 a 中当前区间 [la,ha]，依次用 b 中区间切去重叠段，
// b 完全落在其左侧的区间可永久跳过。
func subtractIntervals(a, b []Interval) []Interval {
	var out []Interval
	j := 0
	for _, x := range a {
		la, ha := x.Lo, x.Hi
		// 跳过完全位于 x 左侧的 b 区间。
		for j < len(b) && b[j].Hi < la {
			j++
		}
		k := j
		cursor := la
		for k < len(b) && b[k].Lo <= ha {
			lb, hb := b[k].Lo, b[k].Hi
			if lb > cursor {
				// cursor 与 lb 之间存在残余前缀。
				out = append(out, Interval{cursor, lb - 1})
			}
			if hb >= cursor {
				// 被切掉一段，游标移到 hb 之后；hb==MaxUint64 时游标回绕为 0。
				cursor = hb + 1
			}
			k++
			if cursor == 0 || cursor > ha {
				break
			}
		}
		if cursor != 0 && cursor <= ha {
			out = append(out, Interval{cursor, ha})
		}
	}
	return out
}
