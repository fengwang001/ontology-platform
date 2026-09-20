package seqwin

import "sync"

// Window 是一个固定宽度的滑动窗口重放检测器，并发安全。
//
// 窗口始终覆盖闭区间 [Highest-size+1, Highest]（在已收到报文之后），
// 内部只保留 size 个槽位的位图，占用内存不随序列号增长。
type Window struct {
	mu      sync.Mutex
	size    uint64
	highest uint64
	bits    []bool
}

// New 创建一个宽度为 size 的窗口；size<=0 时按 1 处理。
func New(size int) *Window {
	if size <= 0 {
		size = 1
	}
	return &Window{
		size: uint64(size),
		bits: make([]bool, size),
	}
}

// Accept 判定序列号 seq，并在判定为 Fresh 时把它记入窗口。
func (w *Window) Accept(seq uint64) Verdict {
	if seq == 0 {
		return Invalid
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.highest == 0 {
		w.highest = seq
		w.bits[(seq-1)%w.size] = true
		return Fresh
	}

	switch {
	case seq == w.highest:
		return Duplicate
	case seq < w.highest:
		if w.highest-seq >= w.size {
			return TooOld
		}
		idx := (seq - 1) % w.size
		if w.bits[idx] {
			return Duplicate
		}
		w.bits[idx] = true
		return Fresh
	default:
		// seq > highest：右推窗口。先清掉被推出左边界的旧槽位，
		// 跳跃跨度不小于窗口宽度时，旧记录全部失效，直接整体清零。
		jump := seq - w.highest
		if jump >= w.size {
			for i := range w.bits {
				w.bits[i] = false
			}
		} else {
			for j := uint64(0); j < jump; j++ {
				// 被逐出的序列号为 highest-size+1+j，其槽位下标等价于
				// (highest+j)%size，这样写可避免 highest<size 时下溢。
				w.bits[(w.highest+j)%w.size] = false
			}
		}
		w.highest = seq
		w.bits[(seq-1)%w.size] = true
		return Fresh
	}
}

// Highest 返回当前见过的最大序列号；未收到任何合法报文时为 0。
func (w *Window) Highest() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.highest
}

// Seen 只查询不记录：序列号 seq 当前是否已在窗口内被记录。
func (w *Window) Seen(seq uint64) bool {
	if seq == 0 {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.highest == 0 || w.highest-seq >= w.size {
		return false
	}
	return w.bits[(seq-1)%w.size]
}

// BitmapLen 返回内部记录结构的槽位数量。
// 该方法仅用于测试断言：槽位数恒等于创建时的窗口宽度，
// 不随见过的序列号数量增长。
func (w *Window) BitmapLen() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.bits)
}
