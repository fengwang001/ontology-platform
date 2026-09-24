// Package cuck 实现布谷鸟过滤器：桶数组、踢出重插入、精确插入集合、满回滚。
package cuck

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hash"
)

var (
	ErrNegativeKey = errors.New("cuck: negative key")
	ErrFull        = errors.New("cuck: filter full")
	ErrNotInserted = errors.New("cuck: delete of non-inserted key")
)

// Filter 是布谷鸟过滤器。零值不可用，请用 New。
type Filter struct {
	mu       sync.RWMutex
	buckets  [][]uint8
	maxKicks int
	inserted map[int64]struct{}
	checked  atomic.Int64 // 最近一次 Lookup/Delete 检查的桶个数（非导出）
}

// New 构造过滤器；参数非法返回 hash.ErrInvalidParams。
func New(numBuckets, entriesPerBucket, maxKicks int) (*Filter, error) {
	if err := hash.Validate(numBuckets, entriesPerBucket, maxKicks); err != nil {
		return nil, err
	}
	b := make([][]uint8, numBuckets)
	for i := range b {
		b[i] = make([]uint8, 0, entriesPerBucket)
	}
	return &Filter{buckets: b, maxKicks: maxKicks, inserted: make(map[int64]struct{})}, nil
}

// cand 返回 x 的两个候选桶。numBuckets>=4 时即 i1 与 i1 XOR o(f)；
// numBuckets=2 时按 numBuckets-1 掩码防越界，对称性 (b^o&m)^o&m==b 仍成立。
func (f *Filter) cand(x int64) (int, int) {
	fp := hash.Fingerprint(x)
	i1 := hash.First(x, len(f.buckets))
	return i1, hash.Alternate(i1, fp) & (len(f.buckets) - 1)
}

// Buckets 返回桶数组的深拷贝快照，供自检与演示核对状态。
func (f *Filter) Buckets() [][]uint8 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([][]uint8, len(f.buckets))
	for i, b := range f.buckets {
		out[i] = append([]uint8(nil), b...)
	}
	return out
}

// Insert 插入键 x。两候选桶都满时从 i1 踢出首条目级联重插入，至多 maxKicks 次；
// 超过则回滚到调用前状态并返回 ErrFull。
func (f *Filter) Insert(x int64) error {
	if x < 0 {
		return ErrNegativeKey
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	snap := make([][]uint8, len(f.buckets))
	for i, b := range f.buckets {
		snap[i] = append([]uint8(nil), b...)
	}
	if err := f.insert(x); err != nil {
		f.buckets = snap
		return err
	}
	f.inserted[x] = struct{}{}
	return nil
}

func (f *Filter) insert(x int64) error {
	fp := hash.Fingerprint(x)
	i1, i2 := f.cand(x)
	for _, b := range []int{i1, i2} {
		if len(f.buckets[b]) < cap(f.buckets[b]) {
			f.buckets[b] = append(f.buckets[b], fp)
			return nil
		}
	}
	// 双满：从 i1 踢出首条目，受害者级联重插入。
	cur, b := fp, i1
	for k := 0; k < f.maxKicks; k++ {
		victim := f.buckets[b][0]
		f.buckets[b][0] = cur
		cur, b = victim, hash.Alternate(b, victim)&(len(f.buckets)-1)
		if len(f.buckets[b]) < cap(f.buckets[b]) {
			f.buckets[b] = append(f.buckets[b], cur)
			return nil
		}
	}
	return ErrFull
}

// Lookup 报告 x 是否可能在过滤器中。只检查 i1/i2 两桶，允许假阳性，绝无假阴性。
func (f *Filter) Lookup(x int64) bool {
	if x < 0 {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	fp := hash.Fingerprint(x)
	i1, i2 := f.cand(x)
	f.checked.Store(2)
	return find(f.buckets[i1], fp) >= 0 || find(f.buckets[i2], fp) >= 0
}

// Delete 删除键 x。x 必须在精确插入集合中，否则返回 ErrNotInserted 且状态不变。
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
	i1, i2 := f.cand(x)
	f.checked.Store(2)
	if i := find(f.buckets[i1], fp); i >= 0 {
		f.buckets[i1] = drop(f.buckets[i1], i)
	} else if i := find(f.buckets[i2], fp); i >= 0 {
		f.buckets[i2] = drop(f.buckets[i2], i)
	} else {
		return ErrNotInserted // 不变量下不可达
	}
	delete(f.inserted, x)
	return nil
}

func find(b []uint8, fp uint8) int {
	for i, v := range b {
		if v == fp {
			return i
		}
	}
	return -1
}

func drop(b []uint8, i int) []uint8 {
	copy(b[i:], b[i+1:])
	return b[:len(b)-1]
}
