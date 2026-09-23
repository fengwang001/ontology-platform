package sched

import (
	"errors"
	"testing"

	"ontology/prog"
)

func mp(src string) *prog.Program {
	p, err := prog.Parse(src)
	if err != nil {
		panic(err)
	}
	return p
}

func TestDbgDeadlock(t *testing.T) {
	s := New()
	_ = s.Submit("A", 1, mp("Lock a\nLock b\nDone"))
	_ = s.Submit("B", 1, mp("Lock b\nLock a\nDone"))
	for i := 0; i < 4; i++ {
		s.Step()
	}
	for _, ev := range s.Trace() {
		t.Logf("step=%d task=%q op=%q arg=%q eff=%v", ev.Step, ev.Task, ev.Op, ev.Arg, ev.Eff)
	}
	t.Logf("errA=%v errB=%v", s.Err("A"), s.Err("B"))
	var dl *DeadlockError
	t.Logf("asB=%v is=%v", errors.As(s.Err("B"), &dl), errors.Is(s.Err("B"), ErrDeadlock))
}

func TestDbgTimeout(t *testing.T) {
	s := New()
	_ = s.Submit("O", 9, mp("Lock m\nDone"))
	_ = s.Submit("W", 1, mp("LockTimeout m 3\nDone"))
	for i := 0; i < 10; i++ {
		s.Step()
	}
	for _, ev := range s.Trace() {
		t.Logf("step=%d task=%q op=%q", ev.Step, ev.Task, ev.Op)
	}
	t.Logf("errW=%v doneO=%v doneW=%v", s.Err("W"), s.Done("O"), s.Done("W"))
}
