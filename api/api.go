// Package api 是 WFQ 的对外门面：并发安全地暴露调度功能。
package api

import (
	"errors"
	"sync"

	"ontology/wfq"
)

// 对外哨兵错误（与 wfq 包同源，互不相同，可用 errors.Is 判定）。
var (
	ErrEmptyWeights = wfq.ErrEmptyWeights
	ErrBadWeight    = wfq.ErrBadWeight
	ErrFlowRange    = wfq.ErrFlowRange
	ErrBadSize      = wfq.ErrBadSize
	ErrEmpty        = wfq.ErrEmpty
)

// WFQ 是并发安全的加权公平队列。
type WFQ struct {
	mu sync.Mutex
	s  *wfq.Scheduler
}

// New 创建 WFQ；weights 非空且每项 >= 1，否则整体失败。
func New(weights []int) (*WFQ, error) {
	s, err := wfq.New(weights)
	if err != nil {
		return nil, err
	}
	return &WFQ{s: s}, nil
}

// Submit 提交一个 size 字节的包到 flow。
func (w *WFQ) Submit(flow, size int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.s.Submit(flow, size)
}

// Dequeue 返回队头完成时刻最小的流下标并推进虚拟时钟。
func (w *WFQ) Dequeue() (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.s.Dequeue()
}

// VirtualTime 返回当前虚拟时钟。
func (w *WFQ) VirtualTime() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.s.VirtualTime()
}

// naive 是朴素参照：每次 Dequeue O(n) 扫描所有非空流取最小完成时刻。
type naive struct {
	w, fin []int
	q      [][]int
	v      int
}

func (n *naive) submit(f, size int) {
	if n.v > n.fin[f] {
		n.fin[f] = n.v
	}
	n.fin[f] += (size + n.w[f] - 1) / n.w[f]
	n.q[f] = append(n.q[f], n.fin[f])
}

func (n *naive) dequeue() (int, bool) {
	b := -1
	for i := range n.q {
		if len(n.q[i]) > 0 && (b < 0 || n.q[i][0] < n.q[b][0]) {
			b = i
		}
	}
	if b < 0 {
		return 0, false
	}
	n.v, n.q[b] = n.q[b][0], n.q[b][1:]
	return b, true
}

// SelfCheck 对内置操作序列逐条核验不变量，返回 5 项结果（nil 为通过）：
// 朴素一致、完成时刻单调、公平性、错误可区分、失败不留痕。只用临时实例，可并发调用。
func (w *WFQ) SelfCheck() []error {
	var errNaive, errMono error
	for seed := int64(1); seed <= 5; seed++ {
		rng := seed
		next := func(m int) int { rng = rng*6364136223846793005 + 1; return int(uint64(rng>>33) % uint64(m)) }
		s, _ := wfq.New([]int{3, 1, 2, 5})
		ref := &naive{w: []int{3, 1, 2, 5}, fin: make([]int, 4), q: make([][]int, 4)}
		prevF := make([]int, 4)
		for op := 0; op < 200; op++ {
			if next(3) > 0 {
				f, sz := next(4), next(9)+1
				s.Submit(f, sz)
				ref.submit(f, sz)
				if s.Finish(f) < prevF[f] || !s.HeadsAscending() {
					errMono = errors.New("finish decreased")
				}
				prevF[f] = s.Finish(f)
			} else {
				got, err := s.Dequeue()
				want, ok := ref.dequeue()
				if (err == nil) != ok || (err == nil && got != want) {
					errNaive = errors.New("order mismatch")
				}
			}
		}
	}
	// 公平性：权重大的流 inc 小、完成时刻小，先出队。
	fair, _ := wfq.New([]int{1, 4})
	fair.Submit(0, 4)
	fair.Submit(1, 4)
	var errFair error
	if g, _ := fair.Dequeue(); g != 1 {
		errFair = errors.New("heavier flow should finish first")
	}
	// 三类错误可区分 + 失败不留痕、被拒后仍可用。
	_, e1 := wfq.New(nil)
	_, e2 := wfq.New([]int{0})
	st, _ := wfq.New([]int{2})
	st.Submit(0, 2)
	e3, e4 := st.Submit(9, 1), st.Submit(0, 0)
	var errDistinct error
	if !errors.Is(e1, ErrEmptyWeights) || !errors.Is(e2, ErrBadWeight) ||
		!errors.Is(e3, ErrFlowRange) || !errors.Is(e4, ErrBadSize) ||
		e1 == e2 || e1 == e3 || e3 == e4 {
		errDistinct = errors.New("sentinels not distinguishable")
	}
	v0, f0, l0 := st.VirtualTime(), st.Finish(0), st.QueueLen(0)
	st.Submit(-1, 1)
	st.Submit(0, -1)
	var errAtomic error
	if st.VirtualTime() != v0 || st.Finish(0) != f0 || st.QueueLen(0) != l0 {
		errAtomic = errors.New("rejected op changed state")
	}
	if _, err := st.Dequeue(); err != nil {
		errAtomic = errors.New("unusable after rejection")
	}
	if _, err := st.Dequeue(); !errors.Is(err, ErrEmpty) {
		errDistinct = errors.New("empty dequeue not rejected")
	}
	return []error{errNaive, errMono, errFair, errDistinct, errAtomic}
}
