// Package cost 实现代价模型：扫描代价、连接代价与中间结果基数估计。
// 所有估计只依赖聚合统计，绝不访问行数据。
package cost

// ScanCost 返回顺序扫描一张 rows 行的表的代价（每行单位代价）。
func ScanCost(rows float64) float64 { return rows }

// JoinCost 返回一次哈希连接的代价：build 左侧 + probe 右侧。
func JoinCost(cardL, cardR float64) float64 { return cardL + cardR }

// TotalCost 累加子计划代价与本次连接代价。
func TotalCost(costL, costR, cardL, cardR float64) float64 {
	return costL + costR + JoinCost(cardL, cardR)
}

// JoinCard 估计连接结果基数：两侧基数之积乘以所有横跨谓词的选择率。
// sels 为空时退化为笛卡尔积。
func JoinCard(cardL, cardR float64, sels ...float64) float64 {
	card := cardL * cardR
	for _, s := range sels {
		card *= s
	}
	return card
}
