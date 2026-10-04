package sched_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/sched"
)

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return len(a) == 0 && len(b) == 0
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestSpecFlow(t *testing.T) {
	s := sched.New(2, 100, 2, 1000)
	mustOK(t, s.Register("d", sched.Params{P: 100, O: 10, W: 5}, 0))
	mustOK(t, s.Enqueue("d", "c1", 60, 1, 200, 0))
	mustOK(t, s.Enqueue("d", "c2", 60, 1, 112, 0))
	mustOK(t, s.Enqueue("d", "c3", 30, 0, 500, 0))
	mustOK(t, s.Enqueue("d", "c4", 40, 2, 12, 0))
	if err := s.Enqueue("d", "c5", 10, 0, 10, 0); !errors.Is(err, sched.ErrUnreachable) {
		t.Fatalf("expire==s: got %v", err)
	}
	r1, err := s.Deliver("d", 12)
	mustOK(t, err)
	if !eqStr(r1.Expired, []string{"c4"}) || !eqStr(r1.IDs, []string{"c1"}) {
		t.Fatalf("D12 %+v", r1)
	}
	r2, _ := s.Deliver("d", 14)
	if len(r2.IDs) != 0 {
		t.Fatalf("D14 %+v", r2)
	}
	mustOK(t, s.Ack("d", "c1", 50))
	r3, _ := s.Deliver("d", 111)
	if !eqStr(r3.IDs, []string{"c2", "c3"}) {
		t.Fatalf("D111 %+v", r3)
	}
	r4, _ := s.Deliver("d", 210)
	if !eqStr(r4.Expired, []string{"c2"}) || !eqStr(r4.IDs, []string{"c3"}) {
		t.Fatalf("D210 %+v", r4)
	}
	r5, _ := s.Deliver("d", 310)
	if !eqStr(r5.Failed, []string{"c3"}) || len(r5.IDs) != 0 {
		t.Fatalf("D310 %+v", r5)
	}
}

func TestReconfigureExample(t *testing.T) {
	s := sched.New(2, 1000, 2, 100)
	mustOK(t, s.Register("d", sched.Params{P: 100, O: 10, W: 5}, 0))
	e, err := s.Reconfigure("d", sched.Params{P: 40, O: 0, W: 10}, 12)
	mustOK(t, err)
	if e != 15 {
		t.Fatalf("e=%d want 15", e)
	}
	mustOK(t, s.Enqueue("d", "a", 10, 0, 500, 13))
	r, err := s.Deliver("d", 13)
	mustOK(t, err)
	if !eqStr(r.IDs, []string{"a"}) {
		t.Fatalf("deliver in grace old window: %+v", r)
	}
	if r2, err := s.Deliver("d", 14); err != nil || len(r2.IDs) != 0 {
		t.Fatalf("same old window after reconf: %+v %v", r2, err)
	}
	if _, err := s.Deliver("d", 39); !errors.Is(err, sched.ErrAsleep) {
		t.Fatalf("39 should be asleep under new params: %v", err)
	}
	// 40 是比旧窗口更后的窗口，故 a 在该窗口重投（第二次）。
	r3, err := s.Deliver("d", 40)
	mustOK(t, err)
	if !eqStr(r3.IDs, []string{"a"}) {
		t.Fatalf("a redelivered in new window 40: %+v", r3)
	}
	if c, _ := s.Lookup("d", "a"); c.Sends != 2 {
		t.Fatalf("a sends after redelivery at 40: %+v", c)
	}
}

func TestRejectionOrder(t *testing.T) {
	s := sched.New(1, 100, 1, 10)
	mustOK(t, s.Register("d", sched.Params{P: 100, O: 0, W: 10}, 50))
	check := func(name string, want, got error) {
		t.Helper()
		if !errors.Is(got, want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}
	check("invalid before clockback", sched.ErrInvalid, s.Register("x", sched.Params{P: 0}, 0))
	check("clockback before nodevice", sched.ErrClockBack, s.Enqueue("ghost", "id", 10, 0, 200, 10))
	check("clockback ack", sched.ErrClockBack, s.Ack("ghost", "id", 10))
	if _, err := s.Deliver("ghost", 10); !errors.Is(err, sched.ErrClockBack) {
		t.Fatalf("clockback deliver: %v", err)
	}
	check("exists", sched.ErrExists, s.Register("d", sched.Params{P: 50, O: 0, W: 10}, 60))
	check("nodevice", sched.ErrNoDevice, s.Enqueue("nope", "a", 10, 0, 200, 60))
	check("nodevice ack", sched.ErrNoDevice, s.Ack("nope", "a", 60))
	if _, err := s.Deliver("d", 60); !errors.Is(err, sched.ErrAsleep) {
		t.Fatalf("asleep: %v", err)
	}
	mustOK(t, s.Enqueue("d", "dup", 10, 0, 2000, 100))
	check("dup before toobig", sched.ErrDupCmd, s.Enqueue("d", "dup", 999, 0, 2000, 100))
	check("ack pending", sched.ErrNoCmd, s.Ack("d", "dup", 100))
	check("ack missing", sched.ErrNoCmd, s.Ack("d", "nope", 100))
	// 上面 now=100 的拒绝未推进时钟，重复 now=100 不构成回退。
	mustOK(t, s.Enqueue("d", "b", 50, 0, 2000, 100))
}

func TestExamined100vs10000(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := sched.New(100, 100, 5, int64(n)+10)
		mustOK(t, s.Register("d", sched.Params{P: 100, O: 0, W: 100}, 0))
		mustOK(t, s.Enqueue("d", "head", 61, 0, 50000, 0))
		for i := 0; i < n-1; i++ {
			mustOK(t, s.Enqueue("d", fmt.Sprintf("c%05d", i), 40, 0, 50000, 0))
		}
		r, err := s.Deliver("d", 0)
		mustOK(t, err)
		ex, _ := s.Examined("d")
		if !eqStr(r.IDs, []string{"head"}) || ex != 2 {
			t.Fatalf("n=%d ids=%v examined=%d", n, r.IDs, ex)
		}
	}
}
