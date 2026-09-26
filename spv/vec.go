// Package spv 维护稀疏向量及其规范形。
package spv

import (
	"sort"
	"sync"
)

// 四类可判定哨兵错误，互不相同。
var (
	// ErrIndexOutOfRange：下标不在 [0,m) 内。
	ErrIndexOutOfRange = sentinel("spv: index out of range")
	// ErrZeroValue：批量构造时显式给出 val==0 的条目（Set(idx,0) 语义是删除，非错误）。
	ErrZeroValue = sentinel("spv: zero value entry")
	// ErrLenMismatch：idx 与 val 长度不一致。
	ErrLenMismatch = sentinel("spv: idx/val length mismatch")
	// ErrNotSorted：下标非严格递增（乱序或重复）。
	ErrNotSorted = sentinel("spv: indices not strictly increasing")
)

type sentinel string

func (e sentinel) Error() string { return string(e) }

// Vec 是规范形稀疏向量：idx 严格递增、无重复；val 恒非零；两者等长。
// 每次 Set 都以 copy-on-write 方式整体替换底层数组，故并发读与写互不竞争。
type Vec struct {
	mu  sync.RWMutex
	m   int
	idx []int
	val []float64
}

// New 创建稠密维度为 m 的空稀疏向量。
func New(m int) *Vec {
	return &Vec{m: m}
}

// M 返回稠密维度。只读。
func (v *Vec) M() int { return v.m }

// Set 设置/覆盖/删除一个条目：val==0 删除该下标，否则覆盖或有序插入。
// 下标越界时整体拒绝，不改变任何状态。
func (v *Vec) Set(index int, value float64) error {
	if index < 0 || index >= v.m {
		return ErrIndexOutOfRange
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	p := sort.SearchInts(v.idx, index)
	if p < len(v.idx) && v.idx[p] == index {
		// 下标已存在：覆盖（非零）或删除（零）。
		if value == 0 {
			v.idx = cut(v.idx, p)
			v.val = cut(v.val, p)
		} else {
			nval := append([]float64(nil), v.val...)
			nval[p] = value
			v.val = nval
		}
		return nil
	}
	// 下标不存在：零值即删除一个本不存在的条目，无操作。
	if value == 0 {
		return nil
	}
	v.idx = insertAt(v.idx, p, index)
	v.val = insertAt(v.val, p, value)
	return nil
}

// Get 取下标对应值，不存在（或越界）返回 0。只读，可并发调用。
func (v *Vec) Get(index int) float64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	p := sort.SearchInts(v.idx, index)
	if p < len(v.idx) && v.idx[p] == index {
		return v.val[p]
	}
	return 0
}

// Len 返回非零条目数。只读。
func (v *Vec) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.idx)
}

// Snapshot 返回内部下标/值切片的只读视图（copy-on-write 下旧数组不再被改写）。
func (v *Vec) Snapshot() (idx []int, val []float64) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.idx, v.val
}

// cut 删除有序切片第 p 个元素，返回全新底层数组。
func cut[T any](s []T, p int) []T {
	out := make([]T, 0, len(s)-1)
	out = append(out, s[:p]...)
	out = append(out, s[p+1:]...)
	return out
}

// insertAt 在有序切片第 p 位插入 x，返回全新底层数组。
func insertAt[T any](s []T, p int, x T) []T {
	out := make([]T, 0, len(s)+1)
	out = append(out, s[:p]...)
	out = append(out, x)
	out = append(out, s[p:]...)
	return out
}
