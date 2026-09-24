// Package api 是限速回放器对外接口，依赖 pace（pace 依赖 tok，方向单向）。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/pace"
	"ontology/tok"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrInvalidParam  = tok.ErrInvalidParam
	ErrClockRollback = tok.ErrClockRollback
	ErrQueueFull     = pace.ErrQueueFull
)

// Pacer 只读方法可多 goroutine 并发；Advance/Enqueue/Emit 单 goroutine 调用。
type Pacer struct {
	mu sync.RWMutex
	q  *pace.Queue
}

func New(rate, catch, burst int64, maxQueue int) (*Pacer, error) {
	q, err := pace.New(rate, catch, burst, maxQueue)
	if err != nil {
		return nil, err
	}
	return &Pacer{q: q}, nil
}
func (p *Pacer) Advance(t int64) error { p.mu.Lock(); defer p.mu.Unlock(); return p.q.Advance(t) }
func (p *Pacer) Enqueue(ids ...int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.q.Enqueue(ids...)
}
func (p *Pacer) Emit() []int64 { p.mu.Lock(); defer p.mu.Unlock(); return p.q.Emit() }
func (p *Pacer) Pending() int  { p.mu.RLock(); defer p.mu.RUnlock(); return p.q.Pending() }
func (p *Pacer) Tokens() int64 { p.mu.RLock(); defer p.mu.RUnlock(); return p.q.Tokens() }
func (p *Pacer) Now() int64    { p.mu.RLock(); defer p.mu.RUnlock(); return p.q.Now() }

type op struct {
	enq     []int64
	adv, em bool
	t       int64
}

// builtin 即第三节的八步金标序列。
func builtin() []op {
	return []op{
		{enq: rng(1, 15)}, {em: true}, {adv: true, t: 1}, {em: true},
		{adv: true, t: 2}, {em: true}, {enq: []int64{16, 17, 18}}, {em: true},
	}
}

// SelfCheck 对内置序列核验四条不变量：
// 朴素一致(1)、追赶封顶(2)、守恒(3)、失败不留痕(4)。
func (p *Pacer) SelfCheck() error { return errors.Join(replay(2, 5, 10, 100), rejections()) }

// replay 让真实队列与朴素参照（从零维护 now/tokens/nq）同跑内置序列逐步比对，
// 并核验每次补充增量恰为 min(burst余量, 选定速率*dt)、入==出+存。
func replay(rate, catch, burst int64, maxQ int) error {
	q, err := pace.New(rate, catch, burst, maxQ)
	if err != nil {
		return err
	}
	var now, tk int64 = 0, burst
	var nq []int64
	var in, out int64
	for i, o := range builtin() {
		var got []int64
		switch {
		case o.enq != nil:
			if err := q.Enqueue(o.enq...); err != nil {
				return err
			}
			nq, in = append(nq, o.enq...), in+int64(len(o.enq))
		case o.adv:
			r := rate
			if len(nq) > 0 { // 追赶判定时刻 = Advance 开始时刻
				r = catch
			}
			t0, inc := tk, int64(0)
			if o.t > now {
				inc = min(burst-tk, r*(o.t-now))
				tk, now = tk+inc, o.t
			}
			if err := q.Advance(o.t); err != nil {
				return err
			}
			if q.Tokens()-t0 != inc { // 不变量2
				return fmt.Errorf("step %d: refill %d != capped %d", i+1, q.Tokens()-t0, inc)
			}
		case o.em:
			got = q.Emit()
			k := int(min(tk, int64(len(nq))))
			want := append([]int64(nil), nq[:k]...)
			nq, tk, out = nq[k:], tk-int64(k), out+int64(k)
			if !slices.Equal(got, want) {
				return fmt.Errorf("step %d: emit %v != naive %v", i+1, got, want)
			}
		}
		if q.Now() != now || q.Tokens() != tk || q.Pending() != len(nq) || in != out+int64(len(nq)) {
			return fmt.Errorf("step %d: triple/conservation mismatch", i+1) // 不变量1、3
		}
	}
	return nil
}

// rejections：三类错误互不相同；被拒后状态不变且实例仍可继续正常使用。
func rejections() error {
	for _, b := range [][4]int64{{0, 5, 10, 4}, {6, 5, 10, 4}, {2, 5, 0, 4}, {2, 5, 10, 0}} {
		if _, err := pace.New(b[0], b[1], b[2], int(b[3])); !errors.Is(err, ErrInvalidParam) {
			return fmt.Errorf("%v: want ErrInvalidParam, got %v", b, err)
		}
	}
	if errors.Is(ErrClockRollback, ErrQueueFull) || errors.Is(ErrClockRollback, ErrInvalidParam) ||
		errors.Is(ErrQueueFull, ErrInvalidParam) {
		return errors.New("sentinel errors are not distinct")
	}
	q, _ := pace.New(2, 5, 10, 3)
	_ = q.Enqueue(1, 2, 3)
	n0, t0, p0 := q.Now(), q.Tokens(), q.Pending()
	if err := q.Advance(-1); !errors.Is(err, ErrClockRollback) {
		return fmt.Errorf("want ErrClockRollback, got %v", err)
	}
	if err := q.Enqueue(4); !errors.Is(err, ErrQueueFull) {
		return fmt.Errorf("want ErrQueueFull, got %v", err)
	}
	if q.Now() != n0 || q.Tokens() != t0 || q.Pending() != p0 {
		return errors.New("rejected op left a trace") // 不变量4
	}
	_ = q.Advance(1)
	if got := q.Emit(); !slices.Equal(got, []int64{1, 2, 3}) {
		return fmt.Errorf("unusable after rejection: %v", got)
	}
	return nil
}

func rng(lo, hi int64) []int64 {
	out := make([]int64, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}
