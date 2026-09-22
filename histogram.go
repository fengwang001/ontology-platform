package ontology

import (
	"math"
	"sync"
)

// Histogram 是区间 [lo, hi) 上的等宽直方图，状态仅存于进程内存，可并发使用。
//
// 每个桶 i 覆盖左闭右开区间 [lo+i*w, lo+(i+1)*w)；最后一个桶同样不含右端，
// 样本恰好等于 hi 计入上溢而不是最后一桶。
type Histogram struct {
	mu sync.Mutex

	lo float64
	hi float64
	n  int
	w  float64

	counts  []uint64
	under   uint64
	over    uint64
	skipped uint64
	added   uint64
}

// NewHistogram 构造 n 个覆盖 [lo, hi) 的等宽桶。
// lo/hi 为 NaN 或 Inf 返回 ErrInvalidBound；lo >= hi 返回 ErrInvalidRange；
// n <= 0 返回 ErrInvalidBuckets。
func NewHistogram(lo, hi float64, n int) (*Histogram, error) {
	if math.IsNaN(lo) || math.IsNaN(hi) || math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return nil, ErrInvalidBound
	}
	if lo >= hi {
		return nil, ErrInvalidRange
	}
	if n <= 0 {
		return nil, ErrInvalidBuckets
	}
	return &Histogram{
		lo:     lo,
		hi:     hi,
		n:      n,
		w:      (hi - lo) / float64(n),
		counts: make([]uint64, n),
	}, nil
}

// Lo 返回区间下界。
func (h *Histogram) Lo() float64 { return h.lo }

// Hi 返回区间上界。
func (h *Histogram) Hi() float64 { return h.hi }

// N 返回桶数。
func (h *Histogram) N() int { return h.n }
