package sched_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/sched"
)

// 判定依据：与朴素模型逐操作比对（错误身份 + 返回清单 + 过期/失败 +
// examined 上界）。所有输入/输出由 t.Logf 打印，go test -v 可见。

func errName(e error) string {
	switch {
	case e == nil:
		return "ok"
	case errors.Is(e, sched.ErrInvalid):
		return "Invalid"
	case errors.Is(e, sched.ErrClockBack):
		return "ClockBack"
	case errors.Is(e, sched.ErrNoDevice):
		return "NoDevice"
	case errors.Is(e, sched.ErrExists):
		return "Exists"
	case errors.Is(e, sched.ErrDupCmd):
		return "DupCmd"
	case errors.Is(e, sched.ErrTooBig):
		return "TooBig"
	case errors.Is(e, sched.ErrUnreachable):
		return "Unreachable"
	case errors.Is(e, sched.ErrFull):
		return "Full"
	case errors.Is(e, sched.ErrNoCmd):
		return "NoCmd"
	case errors.Is(e, sched.ErrAsleep):
		return "Asleep"
	default:
		return e.Error()
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func sl(v []string) string { return fmt.Sprint(v) }

func deliver(t *testing.T, s *sched.Scheduler, now int64) []string {
	t.Helper()
	v, err := s.Deliver("d", now)
	must(t, err)
	return v
}

// 题面例一：过期恰等 / 队首阻塞 / 同窗口不重投 / 重投 / R 次后失败。
func TestSpecExample(t *testing.T) {
	s := sched.New(2, 100, 2, 100)
	must(t, s.Register("d", 100, 10, 5, 0))
	enq := func(id string, size int64, prio int, exp int64) {
		t.Helper()
		must(t, s.Enqueue("d", id, size, prio, exp, 0))
	}
	enq("c1", 60, 1, 200)
	enq("c2", 60, 1, 112)
	enq("c3", 30, 0, 500)
	enq("c4", 40, 2, 12)
	err := s.Enqueue("d", "c5", 10, 0, 10, 0)
	if !errors.Is(err, sched.ErrUnreachable) {
		t.Fatalf("expire=10 want Unreachable, got %v", err)
	}
	t.Logf("输入 enq c5 expire=10 -> 输出 %s（判定：下一可用时刻恰为10，expire≤10不可达）", errName(err))

	if v := deliver(t, s, 12); sl(v) != "[c1]" {
		t.Fatalf("Deliver(12)=%v want [c1]", v)
	}
	st, _ := s.LastSettled("d")
	if sl(st.Expired) != "[c4]" {
		t.Fatalf("c4 expire=12 恰等过期, got %v", st.Expired)
	}
	t.Logf("Deliver(12) -> [c1], Expired=[c4]（判定：c1后剩40字节，c2需60放不下即停，不跳过）")
	if v := deliver(t, s, 14); len(v) != 0 {
		t.Fatalf("same window must not redeliver c1, got %v", v)
	}
	must(t, s.Ack("d", "c1", 50))
	if v := deliver(t, s, 111); sl(v) != "[c2 c3]" {
		t.Fatalf("Deliver(111)=%v want [c2 c3]", v)
	}
	if v := deliver(t, s, 210); sl(v) != "[c3]" {
		t.Fatalf("Deliver(210)=%v want [c3]", v)
	}
	st, _ = s.LastSettled("d")
	if sl(st.Expired) != "[c2]" {
		t.Fatalf("c2 expire=112, got %v", st.Expired)
	}
	if v := deliver(t, s, 310); len(v) != 0 {
		t.Fatalf("Deliver(310)=%v want []", v)
	}
	st, _ = s.LastSettled("d")
	if sl(st.Failed) != "[c3]" {
		t.Fatalf("c3 sent R=2 then failed, got %v", st.Failed)
	}
	t.Logf("Deliver(310) -> [], Failed=[c3]（判定：第2次投递窗口之后的首次Deliver才记失败）")
}

// 题面例二：换参的三种落点。
func TestReconfigureLandings(t *testing.T) {
	newS := func() *sched.Scheduler {
		s := sched.New(2, 100, 2, 100)
		must(t, s.Register("d", 100, 10, 5, 0))
		return s
	}
	// 落点 1：now=12 在旧窗 [10,15) 内，旧窗走完，e=15；新窗 40、80。
	s := newS()
	must(t, s.Reconfigure("d", 40, 0, 10, 12))
	if _, err := s.Deliver("d", 13); err != nil {
		t.Fatalf("13 仍属旧窗: %v", err)
	}
	if _, err := s.Deliver("d", 15); !errors.Is(err, sched.ErrAsleep) {
		t.Fatalf("15 旧窗已闭新窗未到, got %v", err)
	}
	if _, err := s.Deliver("d", 40); err != nil {
		t.Fatalf("首个新窗起点 40: %v", err)
	}
	// 落点 2：now=20 窗外，e=20，首个新窗 40。
	s = newS()
	must(t, s.Reconfigure("d", 40, 0, 10, 20))
	if _, err := s.Deliver("d", 39); !errors.Is(err, sched.ErrAsleep) {
		t.Fatalf("39 want Asleep, got %v", err)
	}
	if _, err := s.Deliver("d", 40); err != nil {
		t.Fatalf("40 new window: %v", err)
	}
	// 落点 3：新参数 P=20,o=0，now=20 恰为起点，算在内。
	s = newS()
	must(t, s.Reconfigure("d", 20, 0, 10, 20))
	if _, err := s.Deliver("d", 20); err != nil {
		t.Fatalf("[20,30) 应算在内: %v", err)
	}
	if _, err := s.Deliver("d", 30); !errors.Is(err, sched.ErrAsleep) {
		t.Fatalf("30 窗间, got %v", err)
	}
}
