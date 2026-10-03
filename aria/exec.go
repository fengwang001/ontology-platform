package aria

// addChecked 返回 a+b 及是否 int64 溢出。
func addChecked(a, b int64) (int64, bool) {
	if b > 0 && a > 9223372036854775807-b {
		return 0, true
	}
	if b < 0 && a < -9223372036854775808-b {
		return 0, true
	}
	return a + b, false
}

// appendUnique 向键列表追加不重复的键，保持首次出现顺序。
func appendUnique(keys []int, k int) []int {
	for _, existing := range keys {
		if existing == k {
			return keys
		}
	}
	return append(keys, k)
}

// execOps 在给定状态上执行一个事务的操作序列。
//
// acc 初值为 0；R(k) 若 k 已在写缓冲则取缓冲值，否则取 state 中的值并
// 把 k 记入读集；W(k,d) 把 acc+d 记入写缓冲并把 k 记入写集。任何一步
// acc 或缓冲值溢出 int64 即返回 overflow=true。rs/ws 按首次出现排序。
func execOps(state []int64, ops []Op) (acc int64, buf map[int]int64, rs, ws []int, overflow bool) {
	buf = make(map[int]int64, len(ops))
	for _, op := range ops {
		switch op.Kind {
		case Read:
			v, buffered := buf[op.Key]
			if !buffered {
				v = state[op.Key]
				rs = appendUnique(rs, op.Key)
			}
			sum, ovf := addChecked(acc, v)
			if ovf {
				return acc, buf, rs, ws, true
			}
			acc = sum
		case Write:
			val, ovf := addChecked(acc, op.Delta)
			if ovf {
				return acc, buf, rs, ws, true
			}
			ws = appendUnique(ws, op.Key)
			buf[op.Key] = val
		}
	}
	return acc, buf, rs, ws, false
}
