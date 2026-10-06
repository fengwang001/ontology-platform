package metering

import (
	"math/big"
	"sort"
)

// mulDiv 返回 floor(a*b/c)，c>0，a、b 非负；用 big.Int 保证不溢出、不丢精度。
func mulDiv(a, b, c int64) int64 {
	z := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return z.Quo(z, big.NewInt(c)).Int64()
}

// usageInRange 计算该表在左闭右开区间 [s, e) 内的用量。
// 读数界定的用量段跨越区间边界时按时刻线性归属，余数计入较晚一侧：
// 段 (t1,t2) 用量 U 在 x 处的累计归属为 floor(U*(x-t1)/(t2-t1))。
// 定位用二分（O(log n) 次比较），扫描只触碰与区间相交的读数段（O(k)），
// 触碰次数计入 st，用于证明开销与历史读数总量无关。
func (m *meter) usageInRange(s, e int64, st *Stats) int64 {
	n := len(m.entries)
	if n < 2 {
		return 0
	}
	idx := sort.Search(n, func(i int) bool {
		st.SearchProbes++
		return m.entries[i].time >= s
	})
	start := idx - 1 // 包含 s 的段可能从 idx-1 开始
	if start < 0 {
		start = 0
	}
	var total int64
	for i := start; i+1 < n; i++ {
		a, b := m.entries[i], m.entries[i+1]
		if a.time >= e {
			break
		}
		st.ScanEntries += 2
		lo := max(s, a.time)
		hi := min(e, b.time)
		if lo >= hi {
			continue
		}
		u, ok := usageBetween(a.leftValue(), b.rightValue(), m.rangeMax)
		if !ok {
			continue // 录入时已校验，不会到达
		}
		d := b.time - a.time
		total += mulDiv(u, hi-a.time, d) - mulDiv(u, lo-a.time, d)
	}
	return total
}
