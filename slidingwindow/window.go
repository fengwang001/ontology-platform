package slidingwindow

import (
	"math"
	"sync"
)

// Window 是固定容量的滑动窗口，使用环形缓冲存放窗口元素，
// 并用一个单调非增的索引双端队列实时维护最大值。
//
// 不变式（持锁时成立）：
//   - vals 为长度等于 capacity 的环形缓冲，head 为最早元素位置，size 为元素数；
//   - deque 中的索引对应的值严格单调递减（新的并列值会替换旧值），
//     队首索引对应的值即窗口最大值；
//   - deque 只引用窗口内索引，元素离窗时队首随之出队；
//   - 每个元素在其生命周期内至多进入 deque 一次，moves 记录累计入队次数。
type Window struct {
	mu       sync.RWMutex
	capacity int
	vals     []float64
	head     int
	size     int
	deque    []int // 环形缓冲索引，对应值严格单调递减
	moves    int64
}

// New 创建容量为 capacity 的空窗口。capacity 小于 1 时返回 ErrInvalidCapacity。
func New(capacity int) (*Window, error) {
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Window{
		capacity: capacity,
		vals:     make([]float64, capacity),
		deque:    make([]int, 0, capacity),
	}, nil
}

// Push 追加一个元素；窗口已满时先自动逐出最早元素。
// 发生自动逐出时 didEvict 为 true，evicted 为被逐出的值。
// v 为 NaN、+Inf 或 -Inf 时返回 ErrInvalidValue，内部状态与搬运计数不变。
func (w *Window) Push(v float64) (evicted float64, didEvict bool, err error) {
	if !isFinite(v) {
		return 0, false, ErrInvalidValue
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size == w.capacity {
		evicted = w.popFront()
		didEvict = true
	}

	idx := (w.head + w.size) % w.capacity
	w.vals[idx] = v
	w.size++
	w.admit(idx, v)
	return evicted, didEvict, nil
}

// PushAll 按顺序批量追加元素，语义等价于逐个 Push，
// 包括满窗时的自动逐出。任一个值非法或切片为空时整批拒绝，
// 内部状态与搬运计数保持调用前不变。
func (w *Window) PushAll(vs []float64) error {
	if len(vs) == 0 {
		return ErrEmptyBatch
	}
	for _, v := range vs {
		if !isFinite(v) {
			return ErrInvalidValue
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, v := range vs {
		if w.size == w.capacity {
			w.popFront()
		}
		idx := (w.head + w.size) % w.capacity
		w.vals[idx] = v
		w.size++
		w.admit(idx, v)
	}
	return nil
}

// Evict 显式逐出并返回最早元素。窗口为空时返回 ErrEmptyEvict，
// 内部状态与搬运计数不变。
func (w *Window) Evict() (float64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size == 0 {
		return 0, ErrEmptyEvict
	}
	return w.popFront(), nil
}

// Max 返回窗口内所有元素的最大值。窗口为空时返回 ErrEmptyMax。
func (w *Window) Max() (float64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.size == 0 {
		return 0, ErrEmptyMax
	}
	return w.vals[w.deque[0]], nil
}

// Snapshot 是窗口内容与最大值在同一时刻的一致性快照。
type Snapshot struct {
	// Values 按从早到晚的顺序保存窗口内容副本。
	Values []float64
	// Max 为同一时刻窗口内的最大值。
	Max float64
}

// Snapshot 原子读取窗口内容与最大值，保证 Max 与逐一扫描 Values
// 得到的最大值一致。窗口为空时返回 ErrEmptyMax。
func (w *Window) Snapshot() (Snapshot, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.size == 0 {
		return Snapshot{}, ErrEmptyMax
	}
	values := make([]float64, w.size)
	for i := 0; i < w.size; i++ {
		values[i] = w.vals[(w.head+i)%w.capacity]
	}
	return Snapshot{Values: values, Max: w.vals[w.deque[0]]}, nil
}

// Len 返回当前窗口内的元素个数（0 到 capacity）。
func (w *Window) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.size
}

// Capacity 返回窗口容量。
func (w *Window) Capacity() int {
	return w.capacity
}

// Moves 返回累计搬运次数：每个元素至多被搬入单调队列一次，
// 因此总次数不超过成功写入的元素总数。被拒绝的操作不增加计数。
func (w *Window) Moves() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.moves
}

// popFront 逐出最早元素，并在其为当前队首时同步出队。调用方须持写锁且窗口非空。
func (w *Window) popFront() float64 {
	v := w.vals[w.head]
	if len(w.deque) > 0 && w.deque[0] == w.head {
		w.deque = w.deque[1:]
	}
	w.head = (w.head + 1) % w.capacity
	w.size--
	return v
}

// admit 将新元素索引纳入单调队列：弹出队尾所有不大于它的元素后入队。
// 使用“<=”弹出并列值，使同值最大值被逐出后仍由更新的并列值保留。
// 每个索引在其生命周期内只被纳入一次。调用方须持写锁。
func (w *Window) admit(idx int, v float64) {
	for len(w.deque) > 0 && w.vals[w.deque[len(w.deque)-1]] <= v {
		w.deque = w.deque[:len(w.deque)-1]
	}
	w.deque = append(w.deque, idx)
	w.moves++
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
