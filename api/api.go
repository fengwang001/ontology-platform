// Package api 对外负载均衡接口：并发安全的 LB 与自检。依赖 svc。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/svc"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrConfig    = errors.New("api: invalid server count")
	ErrIndex     = svc.ErrIndex
	ErrUnderflow = svc.ErrUnderflow
)

// LB 是最少连接负载均衡器，全部方法可并发调用。
type LB struct {
	mu sync.Mutex
	s  *svc.Servers
	n  int
}

// New 构造 n 台服务器的 LB；n < 1 返回 ErrConfig。
func New(n int) (*LB, error) {
	if n < 1 {
		return nil, ErrConfig
	}
	return &LB{s: svc.New(n), n: n}, nil
}

// Pick 返回活动连接数最少、并列下标最小的服务器下标。
func (l *LB) Pick() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Pick()
}

// Acquire 连接建立，下标越界返回 ErrIndex。
func (l *LB) Acquire(i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Acquire(i)
}

// Release 连接结束，下标越界返回 ErrIndex，下溢返回 ErrUnderflow。
func (l *LB) Release(i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Release(i)
}

// Count 返回服务器 i 的当前连接数。
func (l *LB) Count(i int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Count(i)
}

// SelfCheck 对内置操作序列核验四条不变量：与朴素参照一致、计数非负、
// 守恒、失败不留痕。全部通过返回 nil，否则返回指明哪条失败的错误。
// 自检在内部新建的实例上进行，不影响接收者状态。
func (l *LB) SelfCheck() error {
	// 内置操作序列：'a' Acquire, 'r' Release, 'p' Pick；含非法下标与下溢。
	type op struct {
		kind byte
		i    int
	}
	seqs := [][]op{
		{{'a', 0}, {'a', 2}, {'p', 0}, {'a', 1}, {'p', 0}, {'r', 0}, {'p', 0}, {'r', 0}}, // 题目八步
		{{'a', -1}, {'a', 3}, {'r', 5}, {'r', 0}, {'a', 0}, {'a', 0}, {'r', 0}, {'r', 0}, {'r', 0}},
		{{'p', 0}, {'a', 2}, {'a', 2}, {'a', 1}, {'p', 0}, {'r', 2}, {'p', 0}, {'a', 0}, {'r', 1}},
	}
	for si, seq := range seqs {
		lb, err := New(3)
		if err != nil {
			return err
		}
		ref := make([]int, 3)
		acq, rel := 0, 0
		for step, o := range seq {
			var opErr error
			switch o.kind {
			case 'a':
				if opErr = lb.Acquire(o.i); opErr == nil {
					ref[o.i]++
					acq++
				}
			case 'r':
				if opErr = lb.Release(o.i); opErr == nil {
					ref[o.i]--
					rel++
				}
			case 'p':
				want := 0
				for j := 1; j < 3; j++ {
					if ref[j] < ref[want] {
						want = j
					}
				}
				if got := lb.Pick(); got != want { // 不变量 1
					return fmt.Errorf("selfcheck seq%d step%d: Pick=%d, naive=%d", si, step, got, want)
				}
			}
			sum := 0
			for j := 0; j < 3; j++ {
				c, err := lb.Count(j)
				if err != nil {
					return err
				}
				if c != ref[j] { // 不变量 4：失败操作不得改变任何计数
					return fmt.Errorf("selfcheck seq%d step%d: Count(%d)=%d, ref=%d (opErr=%v)", si, step, j, c, ref[j], opErr)
				}
				if c < 0 { // 不变量 2
					return fmt.Errorf("selfcheck seq%d step%d: Count(%d)=%d < 0", si, step, j, c)
				}
				sum += c
			}
			if sum != acq-rel { // 不变量 3
				return fmt.Errorf("selfcheck seq%d step%d: sum=%d, acq-rel=%d", si, step, sum, acq-rel)
			}
		}
	}
	return nil
}
