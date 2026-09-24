// Package cuck 实现布谷鸟过滤器：桶数组、踢出重插入、精确插入集合、满判定与回滚。依赖 hash 包。
package cuck

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hash"
)

// 哨兵错误：ErrNegativeKey 负键；ErrFull 踢出超 maxKicks（已回滚）；ErrNotInserted 键不在精确集合中。
var (
	ErrNegativeKey = errors.New("cuck: negative key")
	ErrFull        = errors.New("cuck: filter full")
	ErrNotInserted = errors.New("cuck: delete of key never inserted")
)

// Filter 是进程内存中的布谷鸟过滤器。Lookup 可并发，Insert/Delete 串行化。
type Filter struct {
	numBuckets, entriesPerBucket, maxKicks int
	buckets                                [][]int // 定长桶，0 表示空槽
	inserted                               map[int64]struct{}
	count                                  int // 净指纹数 = 成功插入数 - 成功删除数
	mu                                     sync.RWMutex
	checked                                atomic.Int64 // 最近一次 Lookup/Delete 检查过的桶个数（非导出）
}

// New 构造过滤器；参数非法时返回 hash.ErrInvalidParams，不产生任何状态。
func New(numBuckets, entriesPerBucket, maxKicks int) (*Filter, error) {
	if !hash.ValidParams(numBuckets, entriesPerBucket, maxKicks) {
		return nil, hash.ErrInvalidParams
	}
	b := make([][]int, numBuckets)
	for i := range b {
		b[i] = make([]int, entriesPerBucket)
	}
	return &Filter{numBuckets: numBuckets, entriesPerBucket: entriesPerBucket, maxKicks: maxKicks, buckets: b, inserted: map[int64]struct{}{}}, nil
}

// alt 返回桶 b 关于指纹 fp 的另一候选桶（掩码在 numBuckets>=4 时为恒等）。
func (f *Filter) alt(b, fp int) int { return hash.Alternate(b, fp) & (f.numBuckets - 1) }
func slot(b []int, fp int) int { // fp 在桶中的下标，或第一个空槽（fp==0），无则 -1
	for i, v := range b {
		if v == fp {
			return i
		}
	}
	return -1
}
func clone(bs [][]int) [][]int {
	out := make([][]int, len(bs))
	for i := range out {
		out[i] = append([]int(nil), bs[i]...)
	}
	return out
}

// Insert 插入键 x。两桶都满时从 i1 踢出首条目并沿 alternate 链重插入，至多 maxKicks 次；
// 仍失败则回滚到插入前状态并返回 ErrFull。
func (f *Filter) Insert(x int64) error {
	if x < 0 {
		return ErrNegativeKey
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fp := hash.Fingerprint(x)
	i1, i2 := hash.I1(x, f.numBuckets), hash.I2(x, f.numBuckets)
	if s := slot(f.buckets[i1], 0); s >= 0 {
		f.buckets[i1][s] = fp
	} else if s := slot(f.buckets[i2], 0); s >= 0 {
		f.buckets[i2][s] = fp
	} else {
		saved := clone(f.buckets) // 回滚快照
		b, cur := i1, fp
		placed := false
		for k := 0; k < f.maxKicks && !placed; k++ {
			victim := f.buckets[b][0] // 踢出首条目，当前指纹排到队尾（保持放入顺序）
			copy(f.buckets[b], f.buckets[b][1:])
			f.buckets[b][len(f.buckets[b])-1] = cur
			cur = victim
			b = f.alt(b, cur)
			if s := slot(f.buckets[b], 0); s >= 0 {
				f.buckets[b][s] = cur
				placed = true
			}
		}
		if !placed {
			f.buckets = saved
			return ErrFull
		}
	}
	f.inserted[x] = struct{}{}
	f.count++
	return nil
}

// Lookup 报告 f(x) 是否出现在 i1(x) 或 i2(x) 桶中。允许假阳性，绝无假阴性。
func (f *Filter) Lookup(x int64) bool {
	if x < 0 {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	fp := hash.Fingerprint(x)
	f.checked.Store(2)
	return slot(f.buckets[hash.I1(x, f.numBuckets)], fp) >= 0 ||
		slot(f.buckets[hash.I2(x, f.numBuckets)], fp) >= 0
}

// Delete 删除已插入的键 x；x 不在精确插入集合中时返回 ErrNotInserted，状态不变。
func (f *Filter) Delete(x int64) error {
	if x < 0 {
		return ErrNegativeKey
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.inserted[x]; !ok {
		return ErrNotInserted
	}
	fp := hash.Fingerprint(x)
	f.checked.Store(2)
	b, s := hash.I1(x, f.numBuckets), -1
	if s = slot(f.buckets[b], fp); s < 0 {
		b = hash.I2(x, f.numBuckets)
		s = slot(f.buckets[b], fp)
	}
	if s < 0 { // 不变量保证不会发生
		return ErrNotInserted
	}
	f.buckets[b][s] = 0
	delete(f.inserted, x)
	f.count--
	return nil
}

// Count 返回净指纹数（成功插入数 − 成功删除数）。
func (f *Filter) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.count
}

// Buckets 返回桶数组的深拷贝，供核验与演示。
func (f *Filter) Buckets() [][]int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return clone(f.buckets)
}
