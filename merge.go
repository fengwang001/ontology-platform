package ontology

import (
	"math"
	"math/bits"
)

// Merge 把 other 的位数组并入 f（按位或）。
//
// 仅当两侧 (m, k) 完全相同时合法，否则返回 *MismatchError
// （可用 errors.Is(err, ErrParamMismatch) 判定），错误信息携带
// 两侧的 (m, k)。参数不一致时 f 与 other 均不被修改。
//
// 同参数下，布隆过滤器的按位或等价于把两批元素插入同一个过滤器，
// 因此合并结果与"合并插入"逐字节相同。other 本身不会被修改。
func (f *Filter) Merge(other *Filter) error {
	if f.m != other.m || f.k != other.k {
		return &MismatchError{
			LeftM: f.m, LeftK: f.k,
			RightM: other.m, RightK: other.k,
		}
	}
	f.mu.Lock()
	other.mu.RLock()
	for i := range f.bits {
		f.bits[i] |= other.bits[i]
	}
	other.mu.RUnlock()
	f.mu.Unlock()
	return nil
}

// EstimateCount 用置位比例反推当前已插入元素个数的估计值。
//
// 设 X 为已置位的比特数，插入 c 个元素后某一位仍为 0 的概率约为
// e^(-k*c/m)，令 1 - X/m = e^(-k*c/m)，解得
//
//	c ≈ -(m/k) * ln(1 - X/m)
//
// 空过滤器返回 0。
func (f *Filter) EstimateCount() float64 {
	f.mu.RLock()
	set := 0
	for _, b := range f.bits {
		set += bits.OnesCount8(b)
	}
	f.mu.RUnlock()
	if set == 0 {
		return 0
	}
	ratio := float64(set) / float64(f.m)
	if ratio >= 1 {
		// 位数组已饱和，估计值发散，返回 +Inf 提示不可用。
		return math.Inf(1)
	}
	return -float64(f.m) / float64(f.k) * math.Log(1-ratio)
}
