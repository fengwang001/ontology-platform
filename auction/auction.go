// Package auction 确定开盘集合竞价的开盘价。
package auction

import "sort"

// Order 是参与定价的委托快照。
type Order struct {
	Buy   bool
	Price int64
	Qty   int64
}

// visited 统计定价阶段访问委托记录的次数（排序比较不计）。
var visited int64

// Price 返回开盘价；最大可成交量为 0 时返回 0。
//
// 候选价只取委托出现过的价格：先按价位聚合买卖量（每条委托记录访问一次），
// 再对 K 个价位做前/后缀和，访问委托记录总数不超过 4N，与价位数 K 无关。
func Price(orders []Order, ref int64) int64 {
	visited = 0
	buyAt := make(map[int64]int64)
	sellAt := make(map[int64]int64)
	for _, o := range orders {
		visited++
		if o.Buy {
			buyAt[o.Price] += o.Qty
		} else {
			sellAt[o.Price] += o.Qty
		}
	}
	prices := make([]int64, 0, len(buyAt)+len(sellAt))
	seen := make(map[int64]bool, len(buyAt)+len(sellAt))
	for p := range buyAt {
		seen[p] = true
		prices = append(prices, p)
	}
	for p := range sellAt {
		if !seen[p] {
			prices = append(prices, p)
		}
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })

	k := len(prices)
	sellPre := make([]int64, k) // 卖价<=prices[i] 的累计量
	buySuf := make([]int64, k)  // 买价>=prices[i] 的累计量
	for i := 0; i < k; i++ {
		sellPre[i] = sellAt[prices[i]]
		if i > 0 {
			sellPre[i] += sellPre[i-1]
		}
	}
	for i := k - 1; i >= 0; i-- {
		buySuf[i] = buyAt[prices[i]]
		if i+1 < k {
			buySuf[i] += buySuf[i+1]
		}
	}

	// 第一遍：最大成交量 bestV 与最小失衡量 bestI。
	bestV := int64(-1)
	bestI := int64(-1)
	for i := 0; i < k; i++ {
		v := min(buySuf[i], sellPre[i])
		d := abs(buySuf[i] - sellPre[i])
		if v > bestV || (v == bestV && d < bestI) {
			bestV, bestI = v, d
		}
	}
	if bestV <= 0 {
		return 0
	}
	// 第二遍：并列集合 T 中全买压取最高价、全卖压取最低价，
	// 其余（方向不一或存在 B=S）取离 ref 最近者，距离相同取较低价。
	hiP, loP, refP := int64(0), int64(0), int64(0)
	allBuy, allSell := true, true
	first := true
	for i := 0; i < k; i++ {
		if min(buySuf[i], sellPre[i]) != bestV || abs(buySuf[i]-sellPre[i]) != bestI {
			continue
		}
		p := prices[i]
		if first {
			hiP, loP, refP = p, p, p
			first = false
		} else {
			hiP = max(hiP, p)
			loP = min(loP, p)
			if closer(p, refP, ref) {
				refP = p
			}
		}
		if buySuf[i] <= sellPre[i] {
			allBuy = false
		}
		if buySuf[i] >= sellPre[i] {
			allSell = false
		}
	}
	switch {
	case allBuy:
		return hiP
	case allSell:
		return loP
	default:
		return refP
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// closer 报告 p 是否比 cur 更接近 ref；距离相同取较低价。
func closer(p, cur, ref int64) bool {
	dp := p - ref
	if dp < 0 {
		dp = -dp
	}
	dc := cur - ref
	if dc < 0 {
		dc = -dc
	}
	return dp < dc || (dp == dc && p < cur)
}
