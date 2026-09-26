// Package bloom 实现位数组与 Add/Test，依赖 hashk 计算位置。
package bloom

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hashk"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrInvalidParam = errors.New("bloom: invalid parameter (m<1 or k<1 or maxAdds<0)")
	ErrEmptyElement = errors.New("bloom: empty element")
	ErrCapacity     = errors.New("bloom: maxAdds exceeded")
)

// Filter 是固定 m 位、k 个哈希函数的布隆过滤器，只增查不删。
type Filter struct {
	m, k     int
	maxAdds  int
	bits     []uint64
	count    int
	lastBits atomic.Int64 // 最近一次成功 Test 检查的位数，非导出，不进公开接口
	mu       sync.RWMutex
}

// New 构造过滤器。参数非法时整体失败，不产生任何状态。
func New(m, k, maxAdds int) (*Filter, error) {
	if m < 1 || k < 1 || maxAdds < 0 {
		return nil, ErrInvalidParam
	}
	return &Filter{m: m, k: k, maxAdds: maxAdds, bits: make([]uint64, (m+63)/64)}, nil
}

// Add 置位 x 的 k 个位。空元素或超容量时整体失败，位数组与计数均不变。
func (f *Filter) Add(x []byte) error {
	if len(x) == 0 {
		return ErrEmptyElement
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.count >= f.maxAdds {
		return ErrCapacity
	}
	for _, p := range hashk.Positions(x, f.m, f.k) {
		f.bits[p/64] |= 1 << (uint(p) % 64)
	}
	f.count++
	return nil
}

// Test 仅当 x 的 k 个位全为 1 才返回 true；只读位数组，无隐藏状态。
func (f *Filter) Test(x []byte) (bool, error) {
	if len(x) == 0 {
		return false, ErrEmptyElement
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	ok, checked := true, 0
	for _, p := range hashk.Positions(x, f.m, f.k) {
		checked++
		if f.bits[p/64]&(1<<(uint(p)%64)) == 0 {
			ok = false
		}
	}
	f.lastBits.Store(int64(checked))
	return ok, nil
}

// Count 返回已成功 Add 的元素个数。
func (f *Filter) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.count
}

// Snapshot 返回位数组快照（true 表示该位为 1），供演示与自检使用。
func (f *Filter) Snapshot() []bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]bool, f.m)
	for i := range out {
		out[i] = f.bits[i/64]&(1<<(uint(i)%64)) != 0
	}
	return out
}
