// Package cbf 在 hash 之上实现删除安全、不会假阴性的计数布隆过滤器：
// 计数器数组 + 每键插入次数的精确计数；插入先做溢出预检、删除先查精确计数，
// 任何被拒绝的操作整体不生效。
package cbf

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hash"
)

// 两类与构造参数非法互不相同的哨兵错误。
var (
	ErrOverflow         = errors.New("cbf: counter overflow at maxCount")
	ErrDeleteUninserted = errors.New("cbf: delete of a key that was never inserted")
)

// Filter 是计数布隆过滤器，全部状态字段均非导出。
type Filter struct {
	mu       sync.RWMutex
	p        hash.Params
	maxCount uint8
	counters []uint8       // m 个计数器，取值恒在 0..maxCount
	counts   map[int64]int // 每键净插入次数（精确 multiset），只保留 >0 的键
	inserts  int           // 成功插入次数
	deletes  int           // 成功删除次数
	visited  atomic.Int64  // 最近一次操作访问过的计数器个数（非导出）
}

// New 构造过滤器；maxCount 为 uint8 天然 <=255，只需拒绝 0。
func New(p hash.Params, maxCount uint8) (*Filter, error) {
	if maxCount < 1 {
		return nil, hash.ErrInvalidParameters
	}
	return &Filter{
		p:        p,
		maxCount: maxCount,
		counters: make([]uint8, p.M()),
		counts:   make(map[int64]int),
	}, nil
}

// Insert 把 x 的 k 个位置各 +1。任一位置已等于 maxCount 则整体拒绝，
// 返回 ErrOverflow，所有计数器与每键计数保持不变。
func (f *Filter) Insert(x int64) error {
	pos := f.p.Positions(x) // 锁外计算，定位只依赖散列
	f.visited.Store(int64(len(pos)))

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range pos { // 先预检：写之前确认没有任何位置溢出
		if f.counters[q] >= f.maxCount {
			return ErrOverflow
		}
	}
	for _, q := range pos {
		f.counters[q]++
	}
	f.counts[x]++
	f.inserts++
	return nil
}

// Delete 要求 x 确实插入过（净次数 > 0），否则返回 ErrDeleteUninserted 且
// 状态不变。合法时把 k 个位置各 −1；由不变量它们必 > 0，不会下溢。
func (f *Filter) Delete(x int64) error {
	pos := f.p.Positions(x)
	f.visited.Store(int64(len(pos)))

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.counts[x] <= 0 { // 以精确计数为准，绝不把假阳性当成插入过
		return ErrDeleteUninserted
	}
	for _, q := range pos {
		f.counters[q]--
	}
	f.counts[x]--
	if f.counts[x] == 0 {
		delete(f.counts, x)
	}
	f.deletes++
	return nil
}

// Contains 报告 k 个位置是否全部 > 0。只读，持 RLock，可并发调用。
func (f *Filter) Contains(x int64) bool {
	pos := f.p.Positions(x)
	f.visited.Store(int64(len(pos)))

	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, q := range pos {
		if f.counters[q] == 0 {
			return false
		}
	}
	return true
}
