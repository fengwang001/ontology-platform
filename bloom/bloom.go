// Package bloom 实现位数组与 Add/Test，依赖 hashk 计算位置。
package bloom

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/hashk"
)

// 哨兵错误，供上层用 errors.Is 判定。
var (
	ErrBadParam = errors.New("bloom: m<1 或 k<1")
	ErrFull     = errors.New("bloom: 元素计数达到 maxAdds")
)

// Filter 是固定 m 位、k 个哈希函数的布隆过滤器，只增查不删。
type Filter struct {
	mu      sync.RWMutex
	bits    []uint64
	m, k    int
	maxAdds int
	count   int
	// lastTestBits 记录最近一次 Test 检查的位数，非导出，不出现在公开接口。
	lastTestBits atomic.Int64
}

// New 构造过滤器；m<1 或 k<1 返回 ErrBadParam，不产生任何状态。
func New(m, k, maxAdds int) (*Filter, error) {
	if m < 1 || k < 1 {
		return nil, ErrBadParam
	}
	return &Filter{bits: make([]uint64, (m+63)/64), m: m, k: k, maxAdds: maxAdds}, nil
}

// Add 置位 x 的 k 个位置；计数达 maxAdds 时整体失败，状态不变。
func (f *Filter) Add(x []byte) error {
	pos := hashk.Positions(x, f.m, f.k)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.count >= f.maxAdds {
		return ErrFull
	}
	for _, p := range pos {
		f.bits[p/64] |= 1 << (uint(p) % 64)
	}
	f.count++
	return nil
}

// Test 当且仅当 x 的 k 个位全为 1 时返回 true；恰好检查 k 位（不短路）。
func (f *Filter) Test(x []byte) bool {
	pos := hashk.Positions(x, f.m, f.k)
	f.mu.RLock()
	all := true
	for _, p := range pos {
		all = all && f.bits[p/64]&(1<<(uint(p)%64)) != 0
	}
	f.mu.RUnlock()
	f.lastTestBits.Store(int64(len(pos)))
	return all
}

// Count 返回已成功 Add 的元素个数。
func (f *Filter) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.count
}

// BitString 返回位 0..m-1 的 0/1 串，供演示打印。
func (f *Filter) BitString() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s := make([]byte, f.m)
	for i := 0; i < f.m; i++ {
		if f.bits[i/64]&(1<<(uint(i)%64)) != 0 {
			s[i] = '1'
		} else {
			s[i] = '0'
		}
	}
	return string(s)
}

// ProbeCostVerified 自检 O(k)：若干档规模下 Add 满 n 个元素后 Test，
// 断言检查位数恒等于 k。只返回判定结论，不暴露计数器数值。
func ProbeCostVerified(k int) bool {
	for _, n := range []int{100, 1000, 10000} {
		f, err := New(n*64, k, n) // 位数组足够大，避免被填满
		if err != nil {
			return false
		}
		for i := 0; i < n; i++ {
			if f.Add([]byte("elem-"+strconv.Itoa(i))) != nil {
				return false
			}
		}
		f.Test([]byte("probe"))
		if int(f.lastTestBits.Load()) != k {
			return false
		}
	}
	return true
}
