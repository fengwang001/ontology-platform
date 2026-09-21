package bloom

import (
	"fmt"
	"math"
	"math/bits"
)

// Merge 把两个同参数过滤器合并为一个新过滤器并返回。
// 要求两侧 m 与 k 完全相同，否则返回包装了 ErrMismatch、
// 并带两边 (m, k) 的错误。两个源过滤器均不被修改。
// 结果等价于把两批元素插入同一个过滤器（位数组按位或）。
func Merge(a, b *Filter) (*Filter, error) {
	if a.m != b.m || a.k != b.k {
		return nil, fmt.Errorf("%w: left (m=%d,k=%d) vs right (m=%d,k=%d)",
			ErrMismatch, a.m, a.k, b.m, b.k)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := &Filter{m: a.m, k: a.k, bits: make([]uint64, len(a.bits))}
	for i := range out.bits {
		out.bits[i] = a.bits[i] | b.bits[i]
	}
	return out, nil
}

// EstimateCount 由置位比例反推当前元素个数的估计值：
//
//	n ≈ -(m/k) * ln(1 - X/m)
//
// 其中 X 为已置位的位数（标准布隆过滤器计数估计公式）。
func (f *Filter) EstimateCount() float64 {
	f.mu.RLock()
	var set uint64
	for _, w := range f.bits {
		set += uint64(bits.OnesCount64(w))
	}
	f.mu.RUnlock()
	if set >= f.m {
		return math.Inf(1)
	}
	return -float64(f.m) / float64(f.k) * math.Log(1-float64(set)/float64(f.m))
}
