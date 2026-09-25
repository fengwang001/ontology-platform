// Package drf 只负责单任务层面的 DRF 判定：
// 主导资源是哪一类、主导比例与主导份额是多少。
// 全部计算使用精确有理数：比较一律交叉相乘（经 math/big 兜底防 int64 溢出），
// 代码中不出现 float64。
package drf

import "math/big"

// Frac 是已约分的正分母分数，D 恒大于 0。
type Frac struct {
	N, D int64
}

// Resource 是两类资源之一。
type Resource int

const (
	// CPU 是第一类资源；两类比例相等时按规则主导资源取 CPU。
	CPU Resource = iota
	// Mem 是第二类资源。
	Mem
)

func b(x int64) *big.Int { return big.NewInt(x) }

// Ratio 返回约分后的 n/d；调用方须保证 d>0。
func Ratio(n, d int64) Frac {
	g := new(big.Int).GCD(nil, nil, b(n), b(d))
	return Frac{N: n / g.Int64(), D: d / g.Int64()}
}

func fromBig(n, d *big.Int) Frac {
	g := new(big.Int).GCD(nil, nil, new(big.Int).Set(n), new(big.Int).Set(d))
	return Frac{N: new(big.Int).Quo(n, g).Int64(), D: new(big.Int).Quo(d, g).Int64()}
}

// Cmp 交叉相乘比较两个分数：-1 / 0 / +1。
func Cmp(x, y Frac) int {
	return new(big.Int).Mul(b(x.N), b(y.D)).Cmp(new(big.Int).Mul(b(y.N), b(x.D)))
}

// Mul 返回 x*y（约分）。
func Mul(x, y Frac) Frac {
	return fromBig(new(big.Int).Mul(b(x.N), b(y.N)), new(big.Int).Mul(b(x.D), b(y.D)))
}

// Add 返回 x+y（约分）。
func Add(x, y Frac) Frac {
	n := new(big.Int).Add(new(big.Int).Mul(b(x.N), b(y.D)), new(big.Int).Mul(b(y.N), b(x.D)))
	return fromBig(n, new(big.Int).Mul(b(x.D), b(y.D)))
}

// Sub 返回 x-y（约分）。
func Sub(x, y Frac) Frac {
	n := new(big.Int).Sub(new(big.Int).Mul(b(x.N), b(y.D)), new(big.Int).Mul(b(y.N), b(x.D)))
	return fromBig(n, new(big.Int).Mul(b(x.D), b(y.D)))
}

// Div 返回 x/y（约分）；调用方须保证 y 非零。
func Div(x, y Frac) Frac {
	return fromBig(new(big.Int).Mul(b(x.N), b(y.D)), new(big.Int).Mul(b(x.D), b(y.N)))
}

// MulInt 返回 x*n（约分）。
func MulInt(x Frac, n int64) Frac {
	return fromBig(new(big.Int).Mul(b(x.N), b(n)), b(x.D))
}

// Dominant 按 cpu/Ccpu 与 mem/Cmem 的交叉相乘比较判定主导资源，相等取 CPU。
// 前置条件：容量为正、cpu 与 mem 非负且不同时为 0（由 api 层校验）。
func Dominant(cpu, mem, cpuCap, memCap int64) Resource {
	// cpu/Ccpu >= mem/Cmem  <=>  cpu*Cmem >= mem*Ccpu
	if new(big.Int).Mul(b(cpu), b(memCap)).Cmp(new(big.Int).Mul(b(mem), b(cpuCap))) >= 0 {
		return CPU
	}
	return Mem
}

// DominantRatio 返回主导资源比例 max(cpu/Ccpu, mem/Cmem)，约分分数。
func DominantRatio(cpu, mem, cpuCap, memCap int64) Frac {
	if Dominant(cpu, mem, cpuCap, memCap) == CPU {
		return Ratio(cpu, cpuCap)
	}
	return Ratio(mem, memCap)
}

// Share 返回分配 a 个单位时的主导份额 a*ratio。
func Share(a, ratio Frac) Frac { return Mul(a, ratio) }
