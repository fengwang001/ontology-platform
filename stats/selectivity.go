package stats

// DefaultSelectivity 是缺失列统计时回退的写死默认选择率。
const DefaultSelectivity = 0.1

// EquiSelectivity 估计一个等值连接谓词 leftCol = rightCol 的选择率，
// 采用 1/max(NDV) 规则。任一列统计缺失时回退默认选择率，并返回 ok=false
// 表示该估计不可靠。整个过程只读取统计元数据，不访问行数据。
func EquiSelectivity(left *Column, right *Column) (sel float64, reliable bool) {
	if left == nil || right == nil || left.NDV <= 0 || right.NDV <= 0 {
		return DefaultSelectivity, false
	}
	ndv := left.NDV
	if right.NDV > ndv {
		ndv = right.NDV
	}
	return 1.0 / float64(ndv), true
}

// CombinePredicates 把同一次连接上多个谓词的选择率按独立性假设相乘。
// 谓词数大于 1 时 independent 为 false，提醒上层标注独立性假设。
func CombinePredicates(sels []float64) (combined float64, independent bool) {
	combined = 1
	for _, s := range sels {
		combined *= s
	}
	return combined, len(sels) <= 1
}
