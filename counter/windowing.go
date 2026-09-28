package counter

// 本文件只包含大窗口 / 子窗口的纯数学计算，不持有任何状态，
// 便于针对负时间戳与边界场景做表驱动测试。

// windowStart 返回时间戳 ts 所属大窗口的起点（左闭）。
// 大窗口为 [start, start+size)，起点满足 start%size == 0；
// 对负时间戳同样向下取整（floor），例如 size=10、ts=-3 时起点为 -10。
func windowStart(ts, size int64) int64 {
	return floorDiv(ts, size) * size
}

// windowEnd 返回时间戳 ts 所属大窗口的终点（右开）。
func windowEnd(ts, size int64) int64 {
	return windowStart(ts, size) + size
}

// firstSubEnd 返回时间戳 ts 在其所属大窗口内对应的最小子窗口终点。
// 子窗口终点为 start+step, start+2*step, ..., start+size；
// 事件时间戳 e 计入所有终点 t（t > e），故最小终点为
// start + floorDiv(e-start, step)*step + step。当 e 恰好落在某个
// 子窗口终点上时，该终点区间左闭右开不含 e，最小终点是“下一个”终点；
// 当 e 恰好落在大窗口起点上时，最小终点为 start+step。
func firstSubEnd(ts, start, step int64) int64 {
	offset := ts - start // ts 属于 [start, start+size)，故 offset >= 0
	return start + floorDiv(offset, step)*step + step
}

// floorDiv 返回整数 floor 除法 a/b 的结果，b 必须为正数。
// Go 原生整除向零取整，对负被除数需要修正为向下取整，
// 例如 -3/10 原生得 0，floor 除法应为 -1。
func floorDiv(a, b int64) int64 {
	q := a / b
	if r := a % b; r != 0 && a < 0 {
		q--
	}
	return q
}
