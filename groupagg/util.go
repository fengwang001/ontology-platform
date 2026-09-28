package groupagg

// itoa 将 int64 转为十进制字符串，供本包的 String 方法使用，避免每个可读表示
// 都走 strconv 的通用路径；同时正确处理 int64 最小值（不能直接 -v）。
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte // int64 最小值占 20 个字符（含负号）
	i := len(buf)
	u := uint64(v)
	neg := v < 0
	if neg {
		u = -u // 二进制补码：MinInt64 时 u == 1<<63，恰好等于其绝对值
	}
	for u != 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
