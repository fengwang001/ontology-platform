// Package api 是对外门面，依赖 trunc（传递依赖 log），依赖方向单向。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/log"
	"ontology/trunc"
)

// 三类可判定哨兵错误（别名到拥有方），互不相同。
var (
	ErrCheckpointInvalid  = log.ErrCheckpointInvalid
	ErrTruncateBeyondCP   = trunc.ErrTruncateBeyondCP
	ErrRecoverOverDeleted = trunc.ErrRecoverOverDeleted
)

// Entry 与 log.Entry 同一类型。
type Entry = log.Entry

// API 组合热日志与截断器，全部状态在进程内存。
type API struct {
	l *log.Log
	t *trunc.Truncator
}

// New 返回空系统。
func New() *API {
	l := log.New()
	return &API{l: l, t: trunc.New(l)}
}

func (a *API) Append(payload string) (uint64, error) { return a.l.Append(payload) }
func (a *API) Checkpoint(off uint64) error           { return a.l.Checkpoint(off) }
func (a *API) Truncate(k uint64) error               { return a.t.Truncate(k) }
func (a *API) Recover(marker, first uint64) error    { return a.t.Recover(marker, first) }
func (a *API) Read(from uint64) []Entry              { return a.t.Read(from) }
func (a *API) CP() int64                             { return a.l.CP() }
func (a *API) Marker() uint64                        { return a.t.Marker() }
func (a *API) First() uint64                         { return a.t.First() }

type snap struct {
	cp int64
	tm uint64
	f  uint64
	es []Entry
}

func (a *API) snapshot() snap {
	return snap{a.CP(), a.Marker(), a.First(), a.Read(0)}
}

// SelfCheck 在一份全新的内部系统上执行内置操作序列核验四条不变量，
// 不触碰接收者状态，任意时刻可并发调用；全部通过返回 nil。
func (a *API) SelfCheck() error {
	c := New()
	ref := map[uint64]string{}
	var fail error
	check := func(step string) { // 不变量 2：可见集 == 朴素参照中 offset>=f 者
		if fail != nil {
			return
		}
		got, f := c.Read(0), c.First()
		if uint64(len(got)) != uint64(len(ref))-f {
			fail = fmt.Errorf("selfcheck %s: length drifts from reference", step)
			return
		}
		for i, e := range got {
			if e.Offset != f+uint64(i) || ref[e.Offset] != e.Payload {
				fail = fmt.Errorf("selfcheck %s: mismatch at %d", step, e.Offset)
				return
			}
		}
	}
	for _, p := range []string{"a", "b", "c"} {
		off, _ := c.Append(p)
		ref[off] = p
		check("append")
	}
	// 第 4/5 步：C(2) 后 T(2) 合法（K<=cp，不变量 1），完成即 tm==f（不变量 3）。
	if fail == nil && (c.Checkpoint(2) != nil || c.CP() != 2 || c.Truncate(2) != nil ||
		c.CP() < int64(c.Marker()) || c.Marker() != 2 || c.First() != 2) {
		fail = fmt.Errorf("selfcheck: checkpoint/truncate invariants broken")
	}
	off, _ := c.Append("d")
	ref[off] = "d"
	// 第 8 步：T(3) 写标记后删除前崩溃，重启读 tm=3、f=2；补删收敛到 tm==f==3。
	if fail == nil && (c.Checkpoint(3) != nil || c.Recover(3, 2) != nil ||
		c.Marker() != 3 || c.First() != 3) {
		fail = fmt.Errorf("selfcheck: interrupted truncation not converged")
	}
	check("recover")
	if fail != nil {
		return fail
	}
	// 不变量 4：三类拒绝互异、不留痕、之后仍可用。prep：0=无 1=追加3条 2=Checkpoint(2)。
	b := New()
	cases := []struct {
		prep int
		want error
		fn   func() error
	}{
		{0, ErrCheckpointInvalid, func() error { return b.Checkpoint(0) }},
		{1, ErrTruncateBeyondCP, func() error { return b.Truncate(3) }},
		{2, ErrTruncateBeyondCP, func() error { return b.Truncate(3) }},
		{0, ErrCheckpointInvalid, func() error { return b.Checkpoint(1) }},
		{0, ErrCheckpointInvalid, func() error { return b.Checkpoint(3) }},
		{0, ErrRecoverOverDeleted, func() error { return b.Recover(2, 3) }},
	}
	for _, r := range cases {
		switch r.prep {
		case 1:
			for _, p := range []string{"x", "y", "z"} {
				b.Append(p)
			}
		case 2:
			b.Checkpoint(2)
		}
		before := b.snapshot()
		err := r.fn()
		if !errors.Is(err, r.want) || !reflect.DeepEqual(before, b.snapshot()) {
			return fmt.Errorf("selfcheck: want %v got %v or rejected op left a trace", r.want, err)
		}
	}
	if ErrCheckpointInvalid == ErrTruncateBeyondCP || ErrCheckpointInvalid == ErrRecoverOverDeleted ||
		ErrTruncateBeyondCP == ErrRecoverOverDeleted {
		return fmt.Errorf("selfcheck: sentinel errors must be distinct")
	}
	if b.Truncate(2) != nil || b.Marker() != 2 || b.First() != 2 {
		return fmt.Errorf("selfcheck: system unusable after rejections")
	}
	return nil
}
