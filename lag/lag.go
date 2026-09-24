// Package lag 实现带滞后上限的读：降级/阻塞两种模式与按冻结 T 排序的等待者最小堆。
package lag

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/fresh"
)

// ErrNegativeLag：Read 的 lag < 0。
var ErrNegativeLag = errors.New("lag: 滞后为负")

// Mode 是读模式。
type Mode int

const (
	Downgrade Mode = iota // 立即返回 A，精确报告是否降级
	Block                 // 阻塞到 A >= 冻结的 T
)

// Result 是读结果：Pos 为读到的位点，Downgraded 表示是否比滞后上限更旧。
type Result struct {
	Pos        int64
	Downgraded bool
}

// waiter 是一个阻塞等待者：ch 在其冻结目标 t 被 Apply 越过后关闭。
type waiter struct {
	t  int64
	ch chan struct{}
}

// waitHeap 是按冻结目标 T 排序的最小堆，保证唤醒只检查堆顶。
type waitHeap []waiter

func (h waitHeap) Len() int           { return len(h) }
func (h waitHeap) Less(i, j int) bool { return h[i].t < h[j].t }
func (h waitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *waitHeap) Push(x any)        { *h = append(*h, x.(waiter)) }
func (h *waitHeap) Pop() any {
	old := *h
	w := old[len(old)-1]
	*h = old[:len(old)-1]
	return w
}

// Reader 包装位点状态，提供两种读模式；持有唯一的锁，保证并发安全。
type Reader struct {
	mu      sync.Mutex
	st      *fresh.State
	w       waitHeap
	checked int // 最近一次 Apply 唤醒时检查过的堆顶条目数；非导出，不进公开接口
}

// NewReader 基于位点状态构造读器。
func NewReader(st *fresh.State) *Reader { return &Reader{st: st} }

// Commit 推进 H；不连续则整体失败。
func (r *Reader) Commit(n int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.Commit(n)
}

// Apply 推进 A 并按堆顶序放行所有 T <= A 的等待者；越界则整体失败。
func (r *Reader) Apply(n int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.st.Apply(n); err != nil {
		return err
	}
	r.checked = 0
	for len(r.w) > 0 {
		r.checked++
		if r.w[0].t > r.st.A {
			break
		}
		close(heap.Pop(&r.w).(waiter).ch)
	}
	return nil
}

// Read 按模式读：T = H - lag 在调用时刻冻结。
func (r *Reader) Read(lagv int64, m Mode) (Result, error) {
	if lagv < 0 {
		return Result{}, ErrNegativeLag
	}
	r.mu.Lock()
	t := r.st.Target(lagv)
	if m == Downgrade {
		res := Result{Pos: r.st.A, Downgraded: r.st.A < t}
		r.mu.Unlock()
		return res, nil
	}
	if r.st.A >= t {
		res := Result{Pos: r.st.A}
		r.mu.Unlock()
		return res, nil
	}
	ch := make(chan struct{})
	heap.Push(&r.w, waiter{t: t, ch: ch})
	r.mu.Unlock()
	<-ch // 等 Apply 把 A 推过本次冻结的 t；此后 H 再推进也不影响
	r.mu.Lock()
	res := Result{Pos: r.st.A}
	r.mu.Unlock()
	return res, nil
}

// Head 返回当前 H。
func (r *Reader) Head() int64 { r.mu.Lock(); defer r.mu.Unlock(); return r.st.H }

// Applied 返回当前 A。
func (r *Reader) Applied() int64 { r.mu.Lock(); defer r.mu.Unlock(); return r.st.A }

// waiters 返回等待者数量，仅供本包白盒测试使用。
func (r *Reader) waiters() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.w) }
