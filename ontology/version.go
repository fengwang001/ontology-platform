package ontology

import "math"

// Version 是全局唯一的逻辑版本号，由 Store 内唯一的序列器单调分配。
// 所有写入、属性定义迁移、基数约束调整都在提交时获得一个 Version，
// 该序列即所有变更的全局串行顺序。"历史时刻"即某个 Version。
type Version uint64

// Open 表示区间右端点开（尚未关闭）。
const Open = Version(math.MaxUint64)

// Interval 是左闭右开的版本区间 [From, To)。To == Open 表示至今有效。
// Interval 一旦发布即不可变；关闭区间通过整体替换（copy-on-write）完成。
type Interval struct {
	From Version
	To   Version
}

// Contains 报告 v 是否落在区间内。
func (iv Interval) Contains(v Version) bool {
	return iv.From <= v && v < iv.To
}

// findInterval 在按 From 升序排列的区间切片中二分查找覆盖 v 的区间。
// 返回区间下标与比较次数（比较次数用于独立验证查找复杂度）。
// 未找到时返回 -1。
func findInterval(ivs []Interval, v Version) (idx int, steps int) {
	lo, hi := 0, len(ivs)-1
	for lo <= hi {
		steps++
		mid := int(uint(lo+hi) >> 1)
		switch {
		case v < ivs[mid].From:
			hi = mid - 1
		case v >= ivs[mid].To && ivs[mid].To != Open:
			lo = mid + 1
		default:
			return mid, steps
		}
	}
	return -1, steps
}
