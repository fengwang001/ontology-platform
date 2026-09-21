package ontology

import "math"

// EstimateCount 按当前置位比例反推过滤器中不同元素的估计个数。
// 设已置位比特数为 X，则估计值为
//
//	n̂ = -(m/k) * ln(1 - X/m)
//
// 这是标准布隆过滤器基数估计公式。位数组接近饱和时估计会
// 急剧放大，全部置位时返回 +Inf。
func (f *Filter) EstimateCount() float64 {
	x := float64(f.setBits())
	m := float64(f.m)
	if x >= m {
		return math.Inf(1)
	}
	return -m / float64(f.k) * math.Log(1-x/m)
}
