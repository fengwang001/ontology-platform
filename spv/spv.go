// Package spv 维护规范形稀疏向量：idx 严格递增、无重复，val 恒非零。
package spv

import (
	"errors"
	"sort"
	"sync"
)

// ErrIndexOutOfRange 表示下标落在 [0,m) 之外。
var ErrIndexOutOfRange = errors.New("spv: index out of range [0,m)")

// Vec 是进程内稀疏向量。零值不可用，请用 New 构造。
type Vec struct {
	mu  sync.RWMutex
	m   int
	idx []int
	val []float64
}

// New 创建稠密维度为 m 的空向量。
func New(m int) *Vec {
	return &Vec{m: m}
}

// Dim 返回稠密维度 m。
func (v *Vec) Dim() int { return v.m }

// Set 写入或覆盖下标 index 的值；val==0 表示删除该下标（不存在则为空操作）。
// 下标越界时整体失败，不改变任何状态。
func (v *Vec) Set(index int, val float64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if index < 0 || index >= v.m {
		return ErrIndexOutOfRange
	}
	k := sort.SearchInts(v.idx, index)
	if k < len(v.idx) && v.idx[k] == index {
		if val == 0 {
			v.idx = append(v.idx[:k], v.idx[k+1:]...)
			v.val = append(v.val[:k], v.val[k+1:]...)
		} else {
			v.val[k] = val
		}
		return nil
	}
	if val == 0 {
		return nil
	}
	v.idx = append(v.idx, 0)
	copy(v.idx[k+1:], v.idx[k:])
	v.idx[k] = index
	v.val = append(v.val, 0)
	copy(v.val[k+1:], v.val[k:])
	v.val[k] = val
	return nil
}

// Get 返回下标 index 的值；不存在或越界均返回 0。Get 只读，可并发调用。
func (v *Vec) Get(index int) float64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if index < 0 || index >= v.m {
		return 0
	}
	k := sort.SearchInts(v.idx, index)
	if k < len(v.idx) && v.idx[k] == index {
		return v.val[k]
	}
	return 0
}

// Snapshot 返回非零条目的拷贝，idx 严格递增、与 val 等长且一一对应。
// 调用方修改返回切片不会影响向量本身。只读，可并发调用。
func (v *Vec) Snapshot() (idx []int, val []float64) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	idx = append([]int(nil), v.idx...)
	val = append([]float64(nil), v.val...)
	return idx, val
}

// Canonical 报告向量是否满足规范形全部条件。
func (v *Vec) Canonical() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.idx) != len(v.val) {
		return false
	}
	prev := -1
	for k, index := range v.idx {
		if index < 0 || index >= v.m || index <= prev || v.val[k] == 0 {
			return false
		}
		prev = index
	}
	return true
}
