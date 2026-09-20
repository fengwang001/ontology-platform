// 本文件实现可复现的加权水塘抽样器。
//
// 算法：A-ExpJ（Efraimidis & Spirakis, 2006，
// "Weighted random sampling with a reservoir"）。
// 对每个权重为 w 的元素，抽取 u ~ Uniform(0,1]，计算键 key = u^(1/w)，
// 水塘始终保留 key 最大的 k 个元素（用最小堆维护堆顶为当前最小 key）。
// 可以证明：任意元素最终留在水塘中的概率与其权重 w 成正比，
// 且该过程是在线的——每个元素只看一遍，水塘之外不缓存任何流数据，
// 因此内存占用严格为 O(k)，与流长度无关。
//
// 可复现性：全部随机性来自构造时显式传入的 uint64 种子，
// 且只发生在 Add 时（每接受一个元素恰好消耗 1 个随机数）。
// Sample 是纯读操作，不消耗随机数、不改变内部状态，
// 因此在没有新增元素时多次 Sample 结果完全一致。
package ontology

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// ErrInvalidCapacity 表示水塘容量 k 不合法（k <= 0）。
var ErrInvalidCapacity = errors.New("ontology: reservoir capacity must be positive")

// ErrInvalidWeight 表示元素权重不合法（非正数、非整数、NaN 或 Inf）。
var ErrInvalidWeight = errors.New("ontology: weight must be a positive integer")

// Sampler 是固定容量 k 的加权水塘抽样器，Add 与 Sample 均并发安全。
type Sampler struct {
	mu       sync.Mutex
	k        int
	r        *rng
	h        minHeap
	seq      uint64
	total    uint64
	rejected uint64
}

// NewSampler 构造容量为 k、由 seed 决定全部随机性的抽样器。
// k <= 0 时返回 ErrInvalidCapacity。
func NewSampler(k int, seed uint64) (*Sampler, error) {
	if k <= 0 {
		return nil, fmt.Errorf("%w, got %d", ErrInvalidCapacity, k)
	}
	return &Sampler{k: k, r: newRNG(seed)}, nil
}

// Add 向流中加入一个元素。weight 必须是正整数值，
// 否则返回 ErrInvalidWeight 且该元素不会进入水塘。
// 每接受一个元素恰好消耗 1 个随机数。
func (s *Sampler) Add(item any, weight float64) error {
	if math.IsNaN(weight) || math.IsInf(weight, 0) ||
		weight <= 0 || weight != math.Trunc(weight) {
		s.mu.Lock()
		s.rejected++
		s.mu.Unlock()
		return fmt.Errorf("%w, got %v", ErrInvalidWeight, weight)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.r.nextFloat64()
	e := entry{item: item, key: math.Pow(u, 1/weight), seq: s.seq}
	s.seq++
	s.total++
	if len(s.h) < s.k {
		heap.Push(&s.h, e)
	} else if e.key > s.h[0].key {
		s.h[0] = e
		heap.Fix(&s.h, 0)
	}
	return nil
}

// Sample 返回当前水塘内容的副本，按元素到达顺序排列。
// 它不消耗随机数、不改变内部状态，返回切片与内部状态互不影响。
func (s *Sampler) Sample() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]entry, len(s.h))
	copy(out, s.h)
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	items := make([]any, len(out))
	for i, e := range out {
		items[i] = e.item
	}
	return items
}

// Size 返回当前水塘中的元素数，任意时刻都不超过 k。
func (s *Sampler) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.h)
}

// Total 返回被接受（权重合法）的元素总数。
func (s *Sampler) Total() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// Rejected 返回因权重非法而被拒绝的元素数。
func (s *Sampler) Rejected() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rejected
}

// RandConsumed 返回至今消耗的随机数个数。
// 同一种子、同一输入序列的两次运行，该计数必然相同。
func (s *Sampler) RandConsumed() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r.consumed()
}
