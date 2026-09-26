// Package cbf 实现计数布隆过滤器的计数器数组：Add / Remove（含下溢保护）/
// Query（取 min）。依赖 ch 包计算哈希下标。调用方须保证 m > 0、k > 0、key ≥ 0。
package cbf

import (
	"errors"
	"sync"

	"ontology/ch"
)

// ErrNotPresent 表示 Remove 的 key 至少有一个计数器为 0（元素不在集合中），
// 为防下溢整体拒绝，不改变任何计数器。
var ErrNotPresent = errors.New("cbf: remove of absent key")

// Filter 是计数布隆过滤器：m 个计数器 + k 个哈希函数，不存原始 key。
type Filter struct {
	mu sync.Mutex // 同时保护 lastVisits，使并发 Query 在 -race 下干净
	c  []int64
	k  int
	// lastVisits 记录最近一次 Query 访问的计数器个数。
	// 非导出，且不提供任何导出读取途径（仅有布尔结论 QueryLocalityOK）。
	lastVisits int
}

// New 构造 m 个计数器、k 个哈希函数的过滤器。
func New(m, k int) *Filter {
	return &Filter{c: make([]int64, m), k: k}
}

// Add 对 key 的 k 个命中计数器各 +1。
func (f *Filter) Add(key int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range ch.Indices(f.k, key, len(f.c)) {
		f.c[i]++
	}
}

// Remove 仅当 key 的 k 个计数器全部 ≥ 1 时各 −1；否则报 ErrNotPresent，
// 不改变任何计数器（先整体校验、再统一扣减，失败不留痕）。
func (f *Filter) Remove(key int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := ch.Indices(f.k, key, len(f.c))
	for _, i := range idx {
		if f.c[i] < 1 {
			return ErrNotPresent
		}
	}
	for _, i := range idx {
		f.c[i]--
	}
	return nil
}

// Query 返回 key 的 k 个命中计数器的最小值（计数下界估计），
// 并把访问的计数器个数（恒为 k）记入 lastVisits。
func (f *Filter) Query(key int64) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := ch.Indices(f.k, key, len(f.c))
	f.lastVisits = len(idx)
	min := f.c[idx[0]]
	for _, i := range idx[1:] {
		if f.c[i] < min {
			min = f.c[i]
		}
	}
	return min
}

// Snapshot 返回计数器数组的副本，供演示与测试核对状态。
func (f *Filter) Snapshot() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.c...)
}

// QueryLocalityOK 报告最近一次 Query 访问的计数器个数是否恰为 k。
// 只暴露布尔结论，计数器数值本身不出现在公开接口。
func (f *Filter) QueryLocalityOK() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastVisits == f.k
}
