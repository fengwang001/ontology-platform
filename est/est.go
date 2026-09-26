// Package est 实现 LogLog 的基数估计：mean 公式与 α_m 查表。依赖 lg。
package est

import (
	"math"

	"ontology/lg"
)

// Estimator 持有寄存器数组并给出基数估计。
type Estimator struct {
	reg   *lg.Registers
	m     int
	alpha float64
	reads int // 非导出计数器：最近一次 Estimate 为计算 mean 访问的寄存器个数
}

// New 返回 m 个寄存器的估计器，m 必须是 2 的幂（由上层保证）。
func New(m int) *Estimator {
	return &Estimator{reg: lg.New(m), m: m, alpha: Alpha(m)}
}

// Add 注入一个元素的 (bucket, z)。
func (e *Estimator) Add(bucket, z int) {
	e.reg.Add(bucket, z)
}

// Estimate 返回 α_m · m · 2^mean，mean = (1/m)·Σ reg[j]，除以全部 m 个寄存器（含 0）。
func (e *Estimator) Estimate() float64 {
	sum := 0
	e.reads = 0
	for j := 0; j < e.m; j++ {
		sum += e.reg.At(j)
		e.reads++
	}
	mean := float64(sum) / float64(e.m)
	return e.alpha * float64(e.m) * math.Exp2(mean)
}

// Registers 返回全部寄存器的副本。
func (e *Estimator) Registers() []int {
	return e.reg.Snapshot()
}

// LastEstimateReadAll 报告最近一次 Estimate 访问的寄存器个数是否恰好为 m。
// 只暴露判定结果，不暴露计数器的数值。
func (e *Estimator) LastEstimateReadAll() bool {
	return e.reads == e.m
}

// Alpha 返回只依赖 m 的 LogLog 偏差修正常数（查表）。
func Alpha(m int) float64 {
	if a, ok := alphaTable[m]; ok {
		return a
	}
	return alphaFormula(m)
}

// alphaTable 预计算 2 的幂对应的 α_m。
var alphaTable = func() map[int]float64 {
	t := make(map[int]float64, 21)
	t[1] = 0.5 // m=1 时 Γ(−1) 是极点，公式退化，取退化常数
	for m := 2; m <= 1<<20; m <<= 1 {
		t[m] = alphaFormula(m)
	}
	return t
}()

// alphaFormula 计算 α_m = (Γ(−1/m)·(1−2^{1/m})/ln2)^(−m)，
// 当 m→∞ 时 α_m → e^(−γ)/√2 ≈ 0.39701（LogLog 标准常数）。
func alphaFormula(m int) float64 {
	x := 1.0 / float64(m)
	v := math.Gamma(-x) * (1 - math.Exp2(x)) / math.Ln2
	return math.Pow(v, -float64(m))
}
