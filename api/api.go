// Package api 是无锁 MPMC 队列的对外接口：New/Enqueue/Dequeue/Len/Close/SelfCheck。
// 依赖 q，反向依赖不允许。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/q"
)

// 可判定哨兵错误，三者互不相同。
var (
	ErrBadMaxLen = errors.New("maxLen must be >= 1")
	ErrFull      = q.ErrFull
	ErrClosed    = q.ErrClosed
)

// Queue 是对外句柄。
type Queue struct{ impl *q.Queue }

// New 校验 maxLen>=1，否则报 ErrBadMaxLen 且不产生任何状态。
func New(maxLen int) (*Queue, error) {
	if maxLen < 1 {
		return nil, ErrBadMaxLen
	}
	return &Queue{impl: q.New(maxLen)}, nil
}

func (qu *Queue) Enqueue(v int) error  { return qu.impl.Enqueue(v) }
func (qu *Queue) Dequeue() (int, bool) { return qu.impl.Dequeue() }
func (qu *Queue) Len() int             { return qu.impl.Len() }
func (qu *Queue) Close() error         { return qu.impl.Close() }

// naiveQ 是 sync.Mutex 保护的朴素切片队列，作参照模型。
type naiveQ struct {
	mu     sync.Mutex
	s      []int
	maxLen int
	closed bool
}

func (n *naiveQ) Enqueue(v int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return ErrClosed
	}
	if len(n.s) >= n.maxLen {
		return ErrFull
	}
	n.s = append(n.s, v)
	return nil
}

func (n *naiveQ) Dequeue() (int, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.s) == 0 {
		return 0, false
	}
	v := n.s[0]
	n.s = n.s[1:]
	return v, true
}

func (n *naiveQ) Len() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.s) }

// SelfCheck 用内置操作序列核验四条不变量，返回首个违例；全部通过返回 nil。
// 只用局部新建队列，可并发调用。
func (qu *Queue) SelfCheck() error {
	// 不变量 1：与朴素参照逐次对拍（确定性 LCG 生成交错序列）
	for _, m := range []int{1, 2, 7, 64} {
		got, _ := New(m)
		ref := &naiveQ{maxLen: m}
		seed := uint32(m*2654435761 + 1)
		for i := 0; i < 400; i++ {
			seed = seed*1664525 + 1013904223
			if seed>>31 == 1 { // Dequeue
				gv, gok := got.Dequeue()
				rv, rok := ref.Dequeue()
				if gv != rv || gok != rok {
					return fmt.Errorf("inv1 dequeue mismatch m=%d i=%d", m, i)
				}
			} else { // Enqueue
				ge := got.Enqueue(i)
				re := ref.Enqueue(i)
				if errors.Is(ge, ErrFull) != errors.Is(re, ErrFull) {
					return fmt.Errorf("inv1 enqueue mismatch m=%d i=%d", m, i)
				}
			}
			if got.Len() != ref.Len() {
				return fmt.Errorf("inv1 len mismatch m=%d i=%d", m, i)
			}
		}
	}
	// 不变量 2：FIFO 且守恒
	fq, _ := New(100)
	for i := 0; i < 100; i++ {
		if err := fq.Enqueue(i); err != nil || fq.Len() != i+1 {
			return fmt.Errorf("inv2 enqueue i=%d", i)
		}
	}
	for i := 0; i < 100; i++ {
		if v, ok := fq.Dequeue(); !ok || v != i || fq.Len() != 99-i {
			return fmt.Errorf("inv2 dequeue i=%d", i)
		}
	}
	// 不变量 3：空/满精确
	eq, _ := New(2)
	if v, ok := eq.Dequeue(); ok || v != 0 {
		return errors.New("inv3 empty dequeue")
	}
	_ = eq.Enqueue(1)
	_ = eq.Enqueue(2)
	if err := eq.Enqueue(3); !errors.Is(err, ErrFull) {
		return errors.New("inv3 full not reported")
	}
	// 不变量 4：失败不留痕
	if _, err := New(0); !errors.Is(err, ErrBadMaxLen) {
		return errors.New("inv4 bad maxLen")
	}
	before := eq.Len()
	if err := eq.Close(); err != nil {
		return errors.New("inv4 close")
	}
	if err := eq.Enqueue(9); !errors.Is(err, ErrClosed) || eq.Len() != before {
		return errors.New("inv4 closed enqueue mutated state")
	}
	if err := eq.Close(); !errors.Is(err, ErrClosed) {
		return errors.New("inv4 double close")
	}
	if v, ok := eq.Dequeue(); !ok || v != 1 { // 关闭后排空，不丢元素
		return errors.New("inv4 drain after close")
	}
	return nil
}
