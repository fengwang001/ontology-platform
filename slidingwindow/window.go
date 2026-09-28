// Package slidingwindow 维护固定容量窗口，并在元素追加/逐出时实时给出窗口内最大值。
package slidingwindow

import (
	"errors"
	"math"
	"sync"
)

// 可区分的拒绝原因。
var (
	// ErrInvalidCapacity 表示构造窗口时容量不是正整数。
	ErrInvalidCapacity = errors.New("slidingwindow: capacity must be positive")
	// ErrEmptyWindow 表示对空窗口执行了逐出或求最大值。
	ErrEmptyWindow = errors.New("slidingwindow: window is empty")
	// ErrInvalidValue 表示写入了非法值（当前仅拒绝 NaN）。
	ErrInvalidValue = errors.New("slidingwindow: value must not be NaN")
)

// Window 是固定容量的滑动窗口，支持并发读取。
type Window struct {
	mu sync.RWMutex

	capacity int

	// ring 为环形缓冲，按进入顺序保存窗口元素；head 指向最早元素。
	ring []entry
	head int
	size int

	// dq 为环形实现的单调非递增双端队列：队首始终是当前窗口最大值。
	// 用 < 比较做尾部挤出，因此相等的旧值保留在新值之前，
	// 旧最大值被逐出时新的并列值自然接管，最大值始终正确。
	dq    []entry
	dqH   int
	dqLen int

	// seq 为已进入窗口的元素分配单调递增序号，用于逐出时匹配队首。
	seq int64

	// moveCount 统计在单调队列尾部被挤出的元素总数；
	// 每个元素至多被挤出一次，这正是均摊 O(1) 的判定依据。
	moveCount int64
}

// New 创建容量为 capacity 的空窗口。
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Window{
		capacity: capacity,
		ring:     make([]entry, capacity),
		dq:       make([]entry, capacity),
	}, nil
}

// entry 同时保存元素值与其进入窗口时的全局序号。
type entry struct {
	seq   int64
	value float64
}

// Append 追加一个值；窗口已满时自动逐出最早元素，返回被自动逐出的值。
func (w *Window) Append(value float64) (evicted float64, evictedPresent bool, err error) {
	if !isValidValue(value) {
		return 0, false, ErrInvalidValue
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if ev, ok := w.evictOldestIfFull(); ok {
		evicted, evictedPresent = ev.value, true
	}
	w.push(value)
	return evicted, evictedPresent, nil
}

// AppendBatch 原子地批量写入；任一值非法则整批不生效。
func (w *Window) AppendBatch(values []float64) (evicted []float64, err error) {
	for _, value := range values {
		if !isValidValue(value) {
			return nil, ErrInvalidValue
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, value := range values {
		if ev, ok := w.evictOldestIfFull(); ok {
			evicted = append(evicted, ev.value)
		}
		w.push(value)
	}
	return evicted, nil
}

// Evict 显式逐出并返回最早进入窗口的元素。
func (w *Window) Evict() (float64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size == 0 {
		return 0, ErrEmptyWindow
	}
	ev := w.evictOldest()
	return ev.value, nil
}

// Max 返回当前窗口内所有元素的最大值。
func (w *Window) Max() (float64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.size == 0 {
		return 0, ErrEmptyWindow
	}
	return w.dq[w.dqH].value, nil
}

// Snapshot 返回与 Max 同一读锁下取得的窗口内容副本。
func (w *Window) Snapshot() ([]float64, float64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.size == 0 {
		return nil, 0, ErrEmptyWindow
	}
	values := make([]float64, w.size)
	for i := 0; i < w.size; i++ {
		values[i] = w.ring[(w.head+i)%w.capacity].value
	}
	return values, w.dq[w.dqH].value, nil
}

// Len 返回窗口当前元素个数。
func (w *Window) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.size
}

// Capacity 返回窗口容量。
func (w *Window) Capacity() int {
	return w.capacity
}

// MoveCount 返回单调队列中累计被搬运（挤出）的元素个数。
func (w *Window) MoveCount() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.moveCount
}

func isValidValue(value float64) bool {
	return !math.IsNaN(value)
}

// push 调用方必须持有写锁。
func (w *Window) push(value float64) {
	w.seq++
	cur := entry{seq: w.seq, value: value}

	idx := (w.head + w.size) % w.capacity
	w.ring[idx] = cur
	w.size++

	// 尾部严格更小的元素不可能再成为最大值，被新元素挤出（一次搬运）；
	// 相等元素保留（旧的在前），保证并列最大值逐出场正确。
	for w.dqLen > 0 && w.dq[(w.dqH+w.dqLen-1)%w.capacity].value < value {
		w.dqLen--
		w.moveCount++
	}
	w.dq[(w.dqH+w.dqLen)%w.capacity] = cur
	w.dqLen++
}

// evictOldestIfFull 在窗口已满时逐出最早元素；调用方必须持有写锁。
func (w *Window) evictOldestIfFull() (entry, bool) {
	if w.size < w.capacity {
		return entry{}, false
	}
	return w.evictOldest(), true
}

// evictOldest 逐出最早元素；调用方必须保证窗口非空并持有写锁。
func (w *Window) evictOldest() entry {
	ev := w.ring[w.head]
	w.head = (w.head + 1) % w.capacity
	w.size--

	// 仅当被逐出元素正是单调队列队首时才需要摘头（O(1)）；
	// 其余情况下它早已在尾部被挤出，无需重扫整窗。
	if w.dqLen > 0 && w.dq[w.dqH].seq == ev.seq {
		w.dqH = (w.dqH + 1) % w.capacity
		w.dqLen--
	}
	return ev
}
