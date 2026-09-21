// Package ontology 提供在线统计量累加器：流式接收 float64 样本，
// 随时读取计数、均值、总体方差与样本方差，并支持把两个独立累加器
// 合并成一个与一次性喂入结果一致的累加器。
//
// 数值稳定性：不使用平方和减均值平方的朴素两遍公式，该公式在大均值
// 小方差数据上会发生灾难性抵消。单样本递推采用 Welford 算法：
//
//	n    = n + 1
//	d1   = x - mean
//	mean = mean + d1/n
//	d2   = x - mean
//	M2   = M2 + d1*d2
//
// M2 是离均差平方和的在线形式，总体方差为 M2/n，样本方差为
// M2/(n-1)。数学上 d1 与 d2 同号或为零，M2 单调不减；读取方差时
// 仍对浮点舍入做下界钳制，保证方差绝不返回负数。
//
// 可合并性：两个累加器 (na, meanA, M2a) 与 (nb, meanB, M2b) 的合并
// 采用 Chan et al. 的平行算法，并写成对 (a, b) 对称的形式，使
// Merge(a, b) 与 Merge(b, a) 逐位相同：
//
//	n     = na + nb
//	mean  = (meanA*na + meanB*nb) / n
//	delta = meanB - meanA
//	M2    = M2a + M2b + delta*delta*na*nb/n
//
// 浮点乘法与加法满足交换律且逐位一致，delta 平方后与符号无关，因此
// 合并结果与参数顺序无关。合并空累加器是恒等操作。
//
// 特殊值：NaN 样本被拒绝并计入跳过计数；正负 Inf 样本会使统计量
// 进入不可用状态，此后读取均值或方差返回 ErrUnavailable 而非 NaN。
package ontology
