// Package topk 提供可撤回的前 K 名维护器。
//
// 排序规则：分数降序；分数相同按标识字典序升序。该全序保证每个元素
// 在任意时刻拥有唯一确定名次，因此结果在相同输入序列下可复现。
package topk

import (
	"sort"
	"sync"
)

// Element 是维护器中的一个元素。
type Element struct {
	ID    string
	Score int64
}

// Maintainer 维护一个固定容量集合中排名前 K 的元素，
// 支持覆盖式新增、幂等撤回与并发只读查询。
type Maintainer struct {
	mu       sync.RWMutex
	k        int
	capacity int
	scores   map[string]int64
}

// New 创建容量为 capacity、取前 k 名的维护器。
func New(k, capacity int) (*Maintainer, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	if capacity < k {
		return nil, ErrCapacityTooSmall
	}
	return &Maintainer{
		k:        k,
		capacity: capacity,
		scores:   make(map[string]int64),
	}, nil
}

// Add 覆盖式写入或更新一个元素；新 ID 加入时不得超容量。
func (m *Maintainer) Add(id string, score int64) error {
	if id == "" {
		return ErrEmptyID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.scores[id]; !exists && len(m.scores) >= m.capacity {
		// 拒绝发生在任何状态修改之前，保证失败不改变状态。
		return ErrCapacityExceeded
	}
	m.scores[id] = score
	return nil
}

// Delete 幂等删除一个元素；不存在时为空操作。
func (m *Maintainer) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.scores, id)
}

// TopK 返回前 K 名（不足 K 个则返回全部）。
func (m *Maintainer) TopK() []Element {
	return m.sorted(m.k)
}

// Top 返回完整有序序列的前 n 名（n <= K，不足则返回全部）。
func (m *Maintainer) Top(n int) ([]Element, error) {
	if n <= 0 {
		return nil, ErrInvalidK
	}
	if n > m.k {
		return nil, ErrCapacityTooSmall
	}
	return m.sorted(n), nil
}

// Len 返回当前元素总数。
func (m *Maintainer) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.scores)
}

// Ordered 返回当前全部元素的完整有序序列快照。
func (m *Maintainer) Ordered() []Element {
	return m.sorted(0)
}

// sorted 在同一把读锁下完成快照拷贝与排序，调用方不得持锁。
// limit <= 0 时返回全部元素。
func (m *Maintainer) sorted(limit int) []Element {
	m.mu.RLock()
	defer m.mu.RUnlock()
	elems := make([]Element, 0, len(m.scores))
	for id, score := range m.scores {
		elems = append(elems, Element{ID: id, Score: score})
	}
	sort.Slice(elems, func(i, j int) bool {
		if elems[i].Score != elems[j].Score {
			return elems[i].Score > elems[j].Score
		}
		return elems[i].ID < elems[j].ID
	})
	if limit > 0 && limit < len(elems) {
		elems = elems[:limit]
	}
	return elems
}
