package rlebitmap

// runLen 返回游程 r 的位数。用 uint64 计算以容纳整域游程
// [0, math.MaxUint32]：其长度为 2^32，超出 uint32 范围。
func runLen(r run) uint64 {
	return uint64(r.hi) - uint64(r.lo) + 1
}

// countRuns 返回游程表示的基数，并统计访问的游程数。
func countRuns(runs []run) (uint64, int) {
	panic("not implemented")
}
