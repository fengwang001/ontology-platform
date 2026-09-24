// Package api 对外接口：New、操作包装、Snapshot、State、SelfCheck。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/uck"
)

var (
	ErrBadChannel = uck.ErrBadChannel
	ErrEmptyQueue = uck.ErrEmptyQueue
	ErrBarrierSeq = uck.ErrBarrierSeq
	ErrBarrierDup = uck.ErrBarrierDup
	ErrStateFull  = uck.ErrStateFull
)

// Op 并发安全的算子句柄。
type Op struct {
	mu sync.Mutex
	o  *uck.Op
}

func New(maxChannelState int) *Op     { return &Op{o: uck.New(maxChannelState)} }
func (a *Op) Arrive(ch, v int) error  { a.mu.Lock(); defer a.mu.Unlock(); return a.o.Arrive(ch, v) }
func (a *Op) Step(ch int) error       { a.mu.Lock(); defer a.mu.Unlock(); return a.o.Step(ch) }
func (a *Op) Barrier(ch, n int) error { a.mu.Lock(); defer a.mu.Unlock(); return a.o.Barrier(ch, n) }
func (a *Op) Restore()                { a.mu.Lock(); defer a.mu.Unlock(); a.o.Restore() }
func (a *Op) RunAll()                 { a.mu.Lock(); defer a.mu.Unlock(); a.o.RunAll() }

type Snapshot = uck.Completed
type State = uck.View

func (a *Op) Snapshot() Snapshot { a.mu.Lock(); defer a.mu.Unlock(); return a.o.Completed() }
func (a *Op) State() State       { a.mu.Lock(); defer a.mu.Unlock(); return a.o.View() }

type Result struct {
	Name string
	Err  error
}

// SelfCheck 对内置操作序列核验四条不变量。
func SelfCheck() []Result {
	return []Result{{"9-step snapshot+restore", check9()},
		{"restore-equivalence", checkRestore()},
		{"reject-no-mutation", checkReject()}}
}

type sop struct{ kind, ch, v int } // kind: 0=Arrive 1=Step 2=Barrier(v=n)
func apply(o *uck.Op, x sop) error {
	switch x.kind {
	case 0:
		return o.Arrive(x.ch, x.v)
	case 1:
		return o.Step(x.ch)
	}
	return o.Barrier(x.ch, x.v)
}
func run(o *uck.Op, s []sop) {
	for _, x := range s {
		apply(o, x)
	}
}

var script9 = []sop{{0, 2, 5}, {1, 2, 0}, {0, 2, 2}, {0, 1, 4}, {2, 1, 1}, {1, 2, 0}, {0, 2, 6}, {0, 1, 7}, {2, 2, 1}}

func viewEq(a, b uck.View) bool {
	return a.Sum == b.Sum && a.Last == b.Last && a.Has == b.Has &&
		slices.Equal(a.Q[0], b.Q[0]) && slices.Equal(a.Q[1], b.Q[1])
}
func check9() error {
	o := uck.New(1 << 20)
	run(o, script9)
	c := o.Completed()
	if c.Sum != [2]int{0, 5} || c.Has != [2]bool{false, true} || c.Last[1] != 5 ||
		!slices.Equal(c.State[0], []int{4}) || !slices.Equal(c.State[1], []int{2, 6}) {
		return fmt.Errorf("snapshot %v/%v cs %v/%v", c.Sum, c.Last, c.State[0], c.State[1])
	}
	o.Restore()
	o.RunAll()
	if v := o.View(); v.Sum != [2]int{11, 13} || v.Last != [2]int{7, 6} {
		return fmt.Errorf("restore+runall Sum/Last=%v/%v", v.Sum, v.Last)
	}
	return nil
}
func checkRestore() error {
	scripts := [][]sop{script9,
		{{0, 1, 1}, {2, 1, 1}, {0, 2, 2}, {2, 2, 1}, {1, 1, 0}, {0, 2, 5}, {0, 1, 6}, {2, 2, 2}, {0, 1, 7}, {2, 1, 2}, {1, 2, 0}, {0, 2, 8}},
		{{0, 1, 3}, {0, 2, 4}, {1, 1, 0}},
	}
	for i, s := range scripts {
		for _, cut := range []int{len(s), len(s) / 2} {
			a, b := uck.New(1<<20), uck.New(1<<20)
			run(a, s[:cut])
			run(b, s[:cut])
			b.Restore()
			run(a, s[cut:])
			run(b, s[cut:])
			a.RunAll()
			b.RunAll()
			if !viewEq(a.View(), b.View()) {
				return fmt.Errorf("script %d cut %d", i, cut)
			}
		}
	}
	return nil
}
func checkReject() error {
	cases := []struct {
		max  int
		ops  []sop
		want error
	}{
		{10, []sop{{0, 3, 1}}, uck.ErrBadChannel},
		{10, []sop{{1, 1, 0}}, uck.ErrEmptyQueue},
		{10, []sop{{2, 1, 2}}, uck.ErrBarrierSeq},
		{10, []sop{{2, 1, 1}, {2, 1, 1}}, uck.ErrBarrierDup},
		{2, []sop{{0, 2, 1}, {2, 1, 1}, {0, 2, 2}, {0, 2, 3}}, uck.ErrStateFull}, // Arrive 时超限
		{1, []sop{{0, 2, 1}, {0, 2, 2}, {2, 1, 1}}, uck.ErrStateFull},            // Barrier 时超限
	}
	for i, c := range cases {
		o := uck.New(c.max)
		var err error
		for _, x := range c.ops {
			err = apply(o, x)
		}
		if !errors.Is(err, c.want) {
			return fmt.Errorf("case %d: got %v", i, err)
		}
	}
	o := uck.New(10)
	run(o, script9)
	before := o.View()
	if err := o.Arrive(9, 1); !errors.Is(err, uck.ErrBadChannel) {
		return fmt.Errorf("bad channel: %v", err)
	}
	if err := o.Barrier(1, 3); !errors.Is(err, uck.ErrBarrierSeq) {
		return fmt.Errorf("barrier seq: %v", err)
	}
	if !viewEq(before, o.View()) {
		return errors.New("rejected op mutated state")
	}
	if err := o.Arrive(1, 9); err != nil || o.Step(1) != nil {
		return errors.New("operator not usable after reject")
	}
	return nil
}
