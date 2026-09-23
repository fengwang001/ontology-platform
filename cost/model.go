package cost

import "math"

// Epsilon 是代价并列判定的相对阈值，推导见 DESIGN.md 第 3 节：
// 它远大于 n≤20 时 float64 乘加累计舍入上界（<5e-14），又远小于真实计划差。
const Epsilon = 1e-12

// Equal 判定两个代价是否在相对误差意义下相等。加 1 的下限用于覆盖空表零代价。
func Equal(a, b float64) bool {
	scale := math.Max(math.Max(math.Abs(a), math.Abs(b)), 1)
	return math.Abs(a-b) <= Epsilon*scale
}

// ScanCost 是单表扫描代价，与行数同阶。
func ScanCost(rows int64) float64 { return float64(rows) }

// JoinCardinality 是两个中间结果按组合选择率 join 后的估计基数。
func JoinCardinality(leftRows, rightRows float64, selectivity float64) float64 {
	return leftRows * rightRows * selectivity
}

// JoinCost 是二元连接代价，与两侧输入规模的乘积成正比；
// cartesian=true 时为笛卡尔积，代价公式相同但由 plan 层单独计数标注。
func JoinCost(leftRows, rightRows float64) float64 { return leftRows * rightRows }

// TreeCost 累加计划总代价 = 扫描代价之和 + 连接代价之和。
func TreeCost(scanCosts, joinCosts []float64) float64 {
	total := 0.0
	for _, c := range scanCosts {
		total += c
	}
	for _, c := range joinCosts {
		total += c
	}
	return total
}

// Prefer 报告候选代价 cand 是否应替换当前最优 cur：更优即替换；
// 在 Equal 判等区间内，用叶子名序列字典序打破并列，名字序更小者胜。
// 返回 true 表示接受候选。
func Prefer(cur, cand float64, curNames, candNames []string) bool {
	if !Equal(cur, cand) {
		return cand < cur
	}
	return lessNames(candNames, curNames)
}

func lessNames(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
