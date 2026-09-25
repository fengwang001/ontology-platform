// Package drf 负责单任务的主导资源判定与主导份额计算，全部使用精确有理数。
// 本包不依赖 alloc/api，依赖方向只允许 api → alloc → drf。
package drf

import "errors"

// Resource 标识一类资源。
type Resource int

const (
	CPU Resource = iota
	Mem
)

// Frac 是已约分的精确分数 N/D，D 恒为正。
type Frac struct{ N, D int64 }

// ErrZeroDenominator 表示构造分数时分母非正。
var ErrZeroDenominator = errors.New("drf: denominator must be positive")

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// NewFrac 构造约分分数；d 必须为正。
func NewFrac(n, d int64) (Frac, error) {
	if d <= 0 {
		return Frac{}, ErrZeroDenominator
	}
	g := gcd(n, d)
	if g == 0 {
		g = 1
	}
	if n == 0 {
		return Frac{0, 1}, nil
	}
	return Frac{n / g, d / g}, nil
}

// Cmp 比较两个分数：-1/0/1（交叉相乘，不引入浮点）。
func Cmp(x, y Frac) int {
	p := x.N * y.D
	q := y.N * x.D
	switch {
	case p < q:
		return -1
	case p > q:
		return 1
	default:
		return 0
	}
}

// Add 返回 x+y。
func Add(x, y Frac) Frac {
	f, _ := NewFrac(x.N*y.D+y.N*x.D, x.D*y.D)
	return f
}

// Sub 返回 x-y。
func Sub(x, y Frac) Frac {
	f, _ := NewFrac(x.N*y.D-y.N*x.D, x.D*y.D)
	return f
}

// Mul 返回 x*y。
func Mul(x, y Frac) Frac {
	f, _ := NewFrac(x.N*y.N, x.D*y.D)
	return f
}

// Div 返回 x/y，y 不得为 0。
func Div(x, y Frac) Frac {
	f, _ := NewFrac(x.N*y.D, x.D*y.N)
	return f
}

// Dominant 判定单任务的主导资源：比较 cpu/cc 与 mem/cm（交叉相乘 cpu*cm vs mem*cc），
// 取更大者，相等取 CPU。前置：cc>0、cm>0、cpu≥0、mem≥0、不同时为 0（由 api 层校验）。
func Dominant(cpu, mem, cc, cm int64) Resource {
	if cpu*cm > mem*cc {
		return CPU
	}
	return Mem
}

// DominantShare 返回 a*max(cpu/cc, mem/cm)，即任务在分配 a 个单位时的主导份额。
func DominantShare(a Frac, cpu, mem, cc, cm int64) Frac {
	var r Frac
	if Dominant(cpu, mem, cc, cm) == CPU {
		r, _ = NewFrac(cpu, cc)
	} else {
		r, _ = NewFrac(mem, cm)
	}
	return Mul(a, r)
}
