// Package api 是无锁 MPMC 队列的对外接口，依赖 q 包。
package api

import (
	"errors"
	"fmt"

	"ontology/q"
)

// 对外暴露的可判定哨兵错误，与 q 包同源，可用 errors.Is 判定。
var (
	ErrBadMaxLen = q.ErrBadMaxLen
	ErrFull      = q.ErrFull
	ErrClosed    = q.ErrClosed
)

// Queue 是对外队列句柄。
type Queue struct {
	impl *q.Q
}

// New 构造队列，maxLen < 1 报 ErrBadMaxLen。
func New(maxLen int) (*Queue, error) {
	impl, err := q.New(maxLen)
	if err != nil {
		return nil, err
	}
	return &Queue{impl: impl}, nil
}

// Enqueue 追加到队尾；满报 ErrFull、已关闭报 ErrClosed，均不改状态。
func (qu *Queue) Enqueue(v int) error { return qu.impl.Enqueue(v) }

// Dequeue 取出队首；空返回 (0, false)。Close 后先排空再恒空。
func (qu *Queue) Dequeue() (int, bool) { return qu.impl.Dequeue() }

// Len 为已入队数减已出队数，O(1)。
func (qu *Queue) Len() int { return qu.impl.Len() }

// Close 置关闭标志（幂等）。
func (qu *Queue) Close() error { return qu.impl.Close() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
// 可被测试直接调用，也可被任意 goroutine 并发调用（只操作自己的队列）。
func SelfCheck() error {
	// 不变量 1+2：FIFO 与守恒，对照朴素参照逐步比对。
	qu, err := New(4)
	if err != nil {
		return err
	}
	var ref []int
	ops := []struct {
		enq bool
		v   int
	}{{true, 1}, {true, 2}, {false, 0}, {true, 3}, {true, 4}, {true, 5},
		{false, 0}, {false, 0}, {true, 6}, {false, 0}, {false, 0}, {false, 0}, {false, 0}}
	for i, op := range ops {
		if op.enq {
			err := qu.Enqueue(op.v)
			if len(ref) == 4 && !errors.Is(err, ErrFull) {
				return fmt.Errorf("selfcheck#1 op%d: want ErrFull got %v", i, err)
			}
			if len(ref) < 4 && err != nil {
				return fmt.Errorf("selfcheck#1 op%d: want nil got %v", i, err)
			}
			if len(ref) < 4 {
				ref = append(ref, op.v)
			}
		} else {
			v, ok := qu.Dequeue()
			if len(ref) == 0 && ok {
				return fmt.Errorf("selfcheck#1 op%d: empty but got (%d,true)", i, v)
			}
			if len(ref) > 0 && (!ok || v != ref[0]) {
				return fmt.Errorf("selfcheck#1 op%d: want (%d,true) got (%d,%v)", i, ref[0], v, ok)
			}
			if len(ref) > 0 {
				ref = ref[1:]
			}
		}
		if qu.Len() != len(ref) {
			return fmt.Errorf("selfcheck#2 op%d: Len=%d want %d", i, qu.Len(), len(ref))
		}
	}
	// 不变量 3：空/满精确。
	qu2, _ := New(1)
	if _, ok := qu2.Dequeue(); ok {
		return errors.New("selfcheck#3: empty dequeue returned ok")
	}
	if err := qu2.Enqueue(7); err != nil {
		return err
	}
	if err := qu2.Enqueue(8); !errors.Is(err, ErrFull) {
		return fmt.Errorf("selfcheck#3: want ErrFull got %v", err)
	}
	// 不变量 4：失败不留痕 + Close 排空。
	if _, err := New(0); !errors.Is(err, ErrBadMaxLen) {
		return fmt.Errorf("selfcheck#4: want ErrBadMaxLen got %v", err)
	}
	qu3, _ := New(2)
	_ = qu3.Enqueue(1)
	_ = qu3.Enqueue(2)
	before := qu3.Len()
	if err := qu3.Enqueue(3); !errors.Is(err, ErrFull) || qu3.Len() != before {
		return errors.New("selfcheck#4: rejected enqueue changed state")
	}
	_ = qu3.Close()
	if err := qu3.Enqueue(9); !errors.Is(err, ErrClosed) {
		return fmt.Errorf("selfcheck#4: want ErrClosed got %v", err)
	}
	if v, ok := qu3.Dequeue(); !ok || v != 1 {
		return errors.New("selfcheck#4: close must drain, lost 1")
	}
	if v, ok := qu3.Dequeue(); !ok || v != 2 {
		return errors.New("selfcheck#4: close must drain, lost 2")
	}
	if _, ok := qu3.Dequeue(); ok {
		return errors.New("selfcheck#4: drained queue must stay empty")
	}
	return nil
}
