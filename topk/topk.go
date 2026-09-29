// Package topk 提供可撤回的前 K 名维护器。
package topk

import (
	"errors"
	"sort"
	"sync"
)

// 可区分的参数错误：调用方可通过 errors.Is 判定具体原因。
var (
	// ErrInvalidK 表示 K 非正。
	ErrInvalidK = errors.New("topk: k must be positive")
	// ErrInvalidCapacity 表示容量小于 K。
	ErrInvalidCapacity = errors.New("topk: capacity must be >= k")
	// ErrEmptyID 表示新增元素的标识为空字符串。
	ErrEmptyID = errors.New("topk: element id must not be empty")
	// ErrCapacityExceeded 表示新增一个不存在的标识会超过容量上限。
	ErrCapacityExceeded = errors.New("topk: capacity exceeded")
)

// Entry 是维护器中的一个元素。
type Entry struct {
	// ID 为元素标识，不允许为空字符串。
	ID string
	// Score 为元素分数；分数越大排名越靠前。
	Score int64
}

// Maintainer 以固定 K 与固定容量维护当前分数最高的若干元素。
//
// 排序规则：分数降序；分数相同时按标识字典序升序，因此任意状态下
// 每个元素都有唯一名次。
type Maintainer struct {
	mu       sync.RWMutex
	k        int
	capacity int
	items    map[string]int64
}

// New 创建一个维护器。
//
// k 为取前 K 名时的 K，capacity 为允许同时存在的元素个数上限。
// k 必须为正且 capacity 不得小于 k，否则分别返回 ErrInvalidK 与
// ErrInvalidCapacity；构造失败时不产生任何实例。
func New(k, capacity int) (*Maintainer, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	if capacity < k {
		return nil, ErrInvalidCapacity
	}
	return &Maintainer{
		k:        k,
		capacity: capacity,
		items:    make(map[string]int64),
	}, nil
}

// Add 新增一个元素，或在标识已存在时覆盖式更新其分数。
//
// 空标识返回 ErrEmptyID；当标识不存在且当前元素数已达容量上限时
// 返回 ErrCapacityExceeded。所有校验在修改状态之前完成，因此失败
// 不会改变任何已有状态。
func (m *Maintainer) Add(id string, score int64) error {
	if id == "" {
		return ErrEmptyID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.items[id]; !exists && len(m.items) >= m.capacity {
		return ErrCapacityExceeded
	}
	m.items[id] = score
	return nil
}

// Delete 删除指定标识的元素，使门槛外排名最高者在下次读取时补位。
// 删除不存在或为空的标识属于幂等空操作，不会报错。
func (m *Maintainer) Delete(id string) {
	if id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, id)
}

// TopK 返回当前前 K 名的副本，按"分数降序、标识字典序升序"排列。
// 元素不足 K 个时返回全部元素。返回的切片可被调用方自由修改。
func (m *Maintainer) TopK() []Entry {
	ordered := m.Ordered()
	if len(ordered) > m.k {
		return ordered[:m.k]
	}
	return ordered
}

// Count 返回当前存活的元素个数。
func (m *Maintainer) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

// Ordered 返回当前全部元素的完整有序序列副本，排序规则与 TopK 相同。
// 任意前 K 名都是该序列的前缀，可用于朴素全量排序交叉核对。
func (m *Maintainer) Ordered() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := make([]Entry, 0, len(m.items))
	for id, score := range m.items {
		entries = append(entries, Entry{ID: id, Score: score})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}
