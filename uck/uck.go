// Package uck 实现双输入算子的非对齐检查点：屏障越过缓冲、在途记录存为通道状态。
package uck

import (
	"errors"
	"sync"

	"ontology/chq"
)

// 可判定哨兵错误，四类互不相同。
var (
	ErrChannel    = errors.New("uck: 通道号非法")
	ErrEmpty      = errors.New("uck: 处理空队列")
	ErrBarrier    = errors.New("uck: 屏障编号或时机非法")
	ErrStateLimit = errors.New("uck: 通道状态超限")
)

// Snap 是算子状态快照：累计和、最近处理值及其存在位。
type Snap struct {
	Sum, Last [3]int
	Has       [3]bool
}

// Operator 是双输入算子，所有方法可并发调用。
type Operator struct {
	mu         sync.Mutex
	ch         [3]*chq.Ch
	cur        Snap // 当前算子状态
	max        int  // 通道状态条数上限
	active     bool // 有进行中的检查点
	n          int  // 进行中检查点编号
	got        [3]bool
	snap, done Snap // 进行中 / 最近完成检查点的快照
	doneN      int  // 最近完成检查点编号，0=无
}

// New 返回算子，maxState 为任一通道通道状态条数上限。
func New(maxState int) *Operator {
	o := &Operator{max: maxState}
	o.ch[1], o.ch[2] = chq.New(), chq.New()
	return o
}

// Arrive 记录到达：追加队尾；记录中的通道同时累积进通道状态。
func (o *Operator) Arrive(ch, v int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if ch != 1 && ch != 2 {
		return ErrChannel
	}
	if o.active && !o.got[ch] && o.ch[ch].StateLen() >= o.max {
		return ErrStateLimit
	}
	o.ch[ch].Push(v)
	return nil
}

// step 处理通道 c 队首一条记录，调用方须持锁；空队列返回 false。
func (o *Operator) step(c int) bool {
	v, ok := o.ch[c].Pop()
	if !ok {
		return false
	}
	o.cur.Sum[c] += v
	o.cur.Last[c], o.cur.Has[c] = v, true
	return true
}

// Step 处理该通道队首一条记录。
func (o *Operator) Step(ch int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if ch != 1 && ch != 2 {
		return ErrChannel
	}
	if !o.step(ch) {
		return ErrEmpty
	}
	return nil
}

// Barrier 检查点 n 的屏障在通道 ch 上到达：越过缓冲、立即转发，不阻塞。
func (o *Operator) Barrier(ch, n int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if ch != 1 && ch != 2 {
		return ErrChannel
	}
	if o.active {
		if n != o.n || o.got[ch] { // 编号不符 / 前一检查点未完成 / 同通道重复
			return ErrBarrier
		}
	} else {
		if n != o.doneN+1 { // 编号必须恰为上一完成编号+1
			return ErrBarrier
		}
		if o.ch[1].Pending() > o.max || o.ch[2].Pending() > o.max {
			return ErrStateLimit
		}
		o.active, o.n, o.got = true, n, [3]bool{}
		o.snap = o.cur
		o.ch[1].Begin()
		o.ch[2].Begin()
	}
	o.got[ch] = true
	o.ch[ch].Barrier()
	if o.got[1] && o.got[2] { // 两通道齐到，检查点完成
		o.active, o.doneN, o.done = false, n, o.snap
		o.ch[1].Commit()
		o.ch[2].Commit()
	}
	return nil
}

// Restore 以最近一次完成的检查点重建状态与队列；无完成检查点则回初始状态。
func (o *Operator) Restore() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cur = o.done // doneN==0 时 done 为零值，即初始状态
	o.ch[1].Restore()
	o.ch[2].Restore()
}

// RunAll 把两个通道的队列全部处理完。
func (o *Operator) RunAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for c := 1; c <= 2; c++ {
		for o.step(c) {
		}
	}
}

// State 返回当前算子状态与两通道未处理记录数。
func (o *Operator) State() (Snap, [3]int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cur, [3]int{0, o.ch[1].Pending(), o.ch[2].Pending()}
}

// Snapshot 返回最近完成检查点的快照、两通道的通道状态与编号。
func (o *Operator) Snapshot() (Snap, []int, []int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.done, o.ch[1].Committed(), o.ch[2].Committed(), o.doneN
}
