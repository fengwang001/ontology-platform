// Package uck 双输入算子：Arrive/Step/Barrier 规则、快照、完成判定、Restore、RunAll。
package uck

import (
	"errors"

	"ontology/chq"
)

var (
	ErrBadChannel = errors.New("uck: channel must be 1 or 2")
	ErrEmptyQueue = errors.New("uck: step on empty queue")
	ErrBarrierSeq = errors.New("uck: barrier number out of sequence")
	ErrBarrierDup = errors.New("uck: duplicate barrier on channel")
	ErrStateFull  = errors.New("uck: channel state limit exceeded")
)

// Op 双输入算子。gen=最近完成检查点号（0=无），act=进行中检查点号（0=无）。
type Op struct {
	ch            [2]chq.Ch
	sum, last     [2]int
	has           [2]bool
	max, gen, act int
	bar           [2]bool
	sSum, sLast   [2]int
	sHas, cHas    [2]bool
	cSum, cLast   [2]int
	cState, cPost [2][]int
}

func New(maxChannelState int) *Op { return &Op{max: maxChannelState} }
func idx(ch int) (int, error) {
	if ch != 1 && ch != 2 {
		return 0, ErrBadChannel
	}
	return ch - 1, nil
}
func (o *Op) Arrive(ch, v int) error {
	i, err := idx(ch)
	if err != nil {
		return err
	}
	rec := o.act > 0 && !o.bar[i]
	if rec && o.ch[i].StateLen() >= o.max {
		return ErrStateFull
	}
	o.ch[i].Arrive(v)
	o.cPost[i] = append(o.cPost[i], v) // 对最近完成检查点而言一律是屏障后记录
	if rec {
		o.ch[i].StateAppend(v)
	} else if o.act > 0 {
		o.ch[i].PostAppend(v) // 已收屏障通道：留给本检查点完成时冻结
	}
	return nil
}
func (o *Op) Step(ch int) error {
	i, err := idx(ch)
	if err != nil {
		return err
	}
	v, ok := o.ch[i].Step()
	if !ok {
		return ErrEmptyQueue
	}
	o.sum[i] += v
	o.last[i], o.has[i] = v, true
	return nil
}
func (o *Op) Barrier(ch, n int) error {
	i, err := idx(ch)
	if err != nil {
		return err
	}
	if o.act == 0 {
		if n != o.gen+1 {
			return ErrBarrierSeq
		}
		if o.ch[0].Len() > o.max || o.ch[1].Len() > o.max {
			return ErrStateFull
		}
		o.act = n
		o.sSum, o.sLast, o.sHas = o.sum, o.last, o.has
		for k := range o.ch {
			o.ch[k].SnapshotState()
			o.ch[k].SetRecording(k != i)
		}
		o.bar = [2]bool{i == 0, i == 1}
		return nil
	}
	if n != o.act {
		return ErrBarrierSeq
	}
	if o.bar[i] {
		return ErrBarrierDup
	}
	o.bar[i] = true
	o.ch[i].SetRecording(false)
	if !o.bar[0] || !o.bar[1] {
		return nil
	}
	o.gen, o.act = o.act, 0
	o.cSum, o.cLast, o.cHas = o.sSum, o.sLast, o.sHas
	for k := range o.ch {
		o.cState[k], o.cPost[k] = o.ch[k].State(), o.ch[k].Post()
		o.ch[k].ClearCheckpoint()
	}
	return nil
}

// Restore 以最近一次完成的检查点重建（gen=0 即初始状态+全部已到达记录）。
func (o *Op) Restore() {
	o.sum, o.last, o.has = o.cSum, o.cLast, o.cHas
	o.act, o.bar = 0, [2]bool{}
	for k := range o.ch {
		q := make([]int, 0, len(o.cState[k])+len(o.cPost[k]))
		q = append(q, o.cState[k]...)
		q = append(q, o.cPost[k]...)
		o.ch[k].Reset(q)
	}
}

// RunAll 把两个通道的队列全部处理完。
func (o *Op) RunAll() {
	for k := range o.ch {
		for v, ok := o.ch[k].Step(); ok; v, ok = o.ch[k].Step() {
			o.sum[k] += v
			o.last[k], o.has[k] = v, true
		}
	}
}

// View 当前算子状态；Completed 最近完成检查点的数据。
type View struct {
	Sum, Last [2]int
	Has       [2]bool
	Q         [2][]int
}
type Completed struct {
	Sum, Last   [2]int
	Has         [2]bool
	State, Post [2][]int
}

func (o *Op) View() View {
	return View{o.sum, o.last, o.has, [2][]int{o.ch[0].Queue(), o.ch[1].Queue()}}
}
func (o *Op) Completed() Completed {
	cp := func(a, b []int) [2][]int { return [2][]int{append([]int(nil), a...), append([]int(nil), b...)} }
	return Completed{o.cSum, o.cLast, o.cHas, cp(o.cState[0], o.cState[1]), cp(o.cPost[0], o.cPost[1])}
}
