// Package topk 提供可撤回的前 K 名维护器。
//
// 元素按 (分数降序, 标识字典序升序) 排定唯一名次；
// 新增为覆盖式更新，删除为幂等空操作；
// 删除门槛内元素后，门槛外名次最高者自动补位。
package topk

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的失败原因，均可用 errors.Is 判定。
var (
	// ErrNonPositiveK 表示 K 非正。
	ErrNonPositiveK = errors.New("topk: K 必须为正整数")
	// ErrCapacityTooSmall 表示容量小于 K。
	ErrCapacityTooSmall = errors.New("topk: 容量必须大于等于 K")
	// ErrEmptyID 表示元素标识不能为空。
	ErrEmptyID = errors.New("topk: 元素标识不能为空")
	// ErrAtCapacity 表示新增时已达容量上限。
	ErrAtCapacity = errors.New("topk: 已达容量上限，无法新增元素")
	// ErrNOutOfRange 表示查询名次数量超出 [1, K]。
	ErrNOutOfRange = errors.New("topk: 查询数量必须落在 [1, K] 区间")
)

// Entry 是被维护的元素：标识 + 分数。
type Entry struct {
	ID    string
	Score float64
}

// less 定义唯一名次：分数降序；分数相同按标识字典序升序。
func less(a, b Entry) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ID < b.ID
}

// TopK 维护分数最高的若干元素，容量上限为 capacity，门槛为 K。
// 零值不可用，请通过 New 构造。并发安全。
type TopK struct {
	mu       sync.RWMutex
	k        int
	capacity int
	scores   map[string]float64
	order    []Entry // 始终按 less 有序，名次即下标
}

// New 构造维护器。K 非正或容量小于 K 时整体拒绝。
func New(k, capacity int) (*TopK, error) {
	if k <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrNonPositiveK, k)
	}
	if capacity < k {
		return nil, fmt.Errorf("%w: K=%d capacity=%d", ErrCapacityTooSmall, k, capacity)
	}
	return &TopK{
		k:        k,
		capacity: capacity,
		scores:   make(map[string]float64),
	}, nil
}

// K 返回构造时设定的门槛。
func (t *TopK) K() int { return t.k }

// Capacity 返回容量上限。
func (t *TopK) Capacity() int { return t.capacity }

// locate 返回 e 在 order 中的下标；不存在时 ok 为 false，
// 且下标为按 less 应插入的位置。
func (t *TopK) locate(e Entry) (int, bool) {
	i := sort.Search(len(t.order), func(i int) bool {
		return !less(t.order[i], e)
	})
	if i < len(t.order) && t.order[i].ID == e.ID {
		return i, true
	}
	return i, false
}

// Upsert 覆盖式新增或更新元素。
// 空标识、或新增时已达容量上限，整体拒绝且状态不变。
func (t *TopK) Upsert(id string, score float64) error {
	if id == "" {
		return ErrEmptyID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.scores[id]; ok {
		if old == score {
			return nil
		}
		idx, _ := t.locate(Entry{ID: id, Score: old})
		t.order = append(t.order[:idx], t.order[idx+1:]...)
	} else {
		if len(t.order) >= t.capacity {
			return fmt.Errorf("%w: capacity=%d id=%q", ErrAtCapacity, t.capacity, id)
		}
	}
	e := Entry{ID: id, Score: score}
	idx, _ := t.locate(e)
	t.order = append(t.order, Entry{})
	copy(t.order[idx+1:], t.order[idx:])
	t.order[idx] = e
	t.scores[id] = score
	return nil
}

// Remove 撤回元素；元素不存在时为幂等空操作。
// 空标识被拒绝且状态不变。
func (t *TopK) Remove(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	score, ok := t.scores[id]
	if !ok {
		return nil
	}
	idx, _ := t.locate(Entry{ID: id, Score: score})
	t.order = append(t.order[:idx], t.order[idx+1:]...)
	delete(t.scores, id)
	return nil
}

// TopK 返回当前前 K 名；不足 K 个时返回全部。
func (t *TopK) TopK() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.head(t.k)
}

// Top 返回当前前 n 名（1 <= n <= K），是完整有序序列的前缀。
func (t *TopK) Top(n int) ([]Entry, error) {
	if n < 1 || n > t.k {
		return nil, fmt.Errorf("%w: n=%d K=%d", ErrNOutOfRange, n, t.k)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.head(n), nil
}

// head 返回有序序列前 n 名的副本，调用方须持有读锁。
func (t *TopK) head(n int) []Entry {
	if n > len(t.order) {
		n = len(t.order)
	}
	out := make([]Entry, n)
	copy(out, t.order[:n])
	return out
}

// Count 返回当前被维护的元素个数。
func (t *TopK) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.order)
}
