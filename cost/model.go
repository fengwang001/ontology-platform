package cost

import "math"

// ScanCost 估计扫描代价：每行 1 个 CPU 单位 + 每 100 行一个 I/O 页（页代价 10）。
func ScanCost(rows int64) float64 {
	pages := math.Ceil(float64(rows) / 100.0)
	return float64(rows) + 10.0*pages
}

// JoinCost 在两子计划代价之上叠加哈希连接代价：
// 较小侧建表（2 倍权重），较大侧探测（1 倍权重）。
func JoinCost(costL, costR, cardL, cardR float64) float64 {
	build, probe := cardL, cardR
	if build > probe {
		build, probe = probe, build
	}
	return costL + costR + 2.0*build + probe
}

// JoinCardinality 用选择率乘积估计连接基数。
func JoinCardinality(cardL, cardR float64, selectivities []float64) float64 {
	card := cardL * cardR
	for _, sel := range selectivities {
		card *= sel
	}
	return card
}
