package ontology

import (
	"math"
	"sort"
	"sync"
)

// TopK 是固定容量 K 的流式 Top-K 选择器，状态仅存于进程内存。
type TopK struct {
	mu      sync.RWMutex
	dir     Direction
	k       int
	items   map[string]Element
	hp      heap
	index   map[string]int
	skipped int
}

// New 构造一个容量为 k、按 dir 方向截取的选择器。
func New(k int, dir Direction) (*TopK, error) {
	if k <= 0 {
		return nil, ErrInvalidCapacity
	}
	if dir != Desc && dir != Asc {
		return nil, ErrInvalidDirection
	}
	return &TopK{
		dir:   dir,
		k:     k,
		items: make(map[string]Element),
		index: make(map[string]int),
		hp:    heap{dir: dir},
	}, nil
}

// Push 推入一个元素。NaN 分数会被拒绝并计入跳过计数；
// 同一 ID 后到的分数覆盖先到的分数，并立即重排。
func (t *TopK) Push(id string, score float64) {
	if math.IsNaN(score) {
		t.mu.Lock()
		t.skipped++
		t.mu.Unlock()
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	incoming := Element{ID: id, Score: score}
	if pos, ok := t.index[id]; ok {
		t.replace(pos, incoming)
		return
	}
	if len(t.index) < t.k {
		t.heapPush(incoming)
		return
	}
	// 已满：与当前最差元素比较，严格更优才替换；
	// 并列时保留 ID 字典序更小者。
	worst := t.hp.items[0]
	if rankLess(t.dir, incoming, worst) {
		t.heapRemove(0)
		t.heapPush(incoming)
	}
}

// Snapshot 返回当前严格按复合次序排好的前 K 个元素；
// 返回切片与内部状态互不影响。
func (t *TopK) Snapshot() []Element {
	t.mu.RLock()
	out := make([]Element, 0, len(t.index))
	for _, e := range t.items {
		out = append(out, e)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return rankLess(t.dir, out[i], out[j])
	})
	return out
}

// Len 返回内部当前持有的元素个数（恒不超过 K）。
func (t *TopK) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.index)
}

// Skipped 返回因 NaN 被拒绝的推入次数。
func (t *TopK) Skipped() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.skipped
}
