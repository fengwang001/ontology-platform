// Package tbucket 提供负安全的时间桶归属计算：floor 除法、桶键与桶区间判定。
package tbucket

// FloorDiv 返回 floor(a/b)，要求 b>0；对负 a 也向下取整（Go 的 / 向零截断，不能直接用）。
func FloorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// Key 返回时间戳 ts 在桶宽 size 下落入的桶键。
func Key(ts, size int64) int64 { return FloorDiv(ts, size) }

// Contains 判定 ts 是否落在桶 k 的左闭右开区间 [k*size, (k+1)*size)。
func Contains(k, ts, size int64) bool { return Key(ts, size) == k }
