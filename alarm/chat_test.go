package alarm_test

import (
	"testing"

	"ontology/alarm"
)

// 震荡窗口左开右闭：window=10, count=3。
// 进入激活时刻 0、10、11 时，窗口 (1,11] 只含 10、11（0 在左边界被排除），
// 不自动屏蔽；再于 t=12 进入激活，窗口 (2,12] 含 10、11、12 共 3 次，自动屏蔽。
func TestChatteringWindowBoundary(t *testing.T) {
	cfg := testCfg() // window 10, count 3, auto-shelve 5
	s := alarm.New(cfg, []alarm.PointConfig{{ID: 9, Priority: alarm.PriorityHigh}}, testLogger(t))

	step := func(tr func() error) {
		t.Helper()
		if err := tr(); err != nil {
			t.Fatal(err)
		}
	}
	enterActive := func(ts int) {
		step(func() error { return s.Trigger(ts, 9) })
	}
	leave := func(ts int) {
		step(func() error { return s.Return(ts, 9) })                        // active-unacked -> return-unacked
		step(func() error { return s.Ack(ts, 9, alarm.RoleOperator, "op") }) // -> normal
	}

	enterActive(0)
	leave(1)
	enterActive(10)
	leave(10)
	// 时刻 11 进入激活：窗口 (1,11] 含 10、11，0 被左开边界排除 -> 2 次。
	enterActive(11)
	sh, _, _, err := s.ShelvingOf(11, 9)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Active {
		t.Fatalf("left-open boundary: t=0 excluded, must not shelve, got %+v", sh)
	}

	leave(12)
	enterActive(12) // 窗口 (2,12]：10、11、12 -> 3 次
	sh, st, _, err := s.ShelvingOf(12, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !sh.Active || sh.Manual || sh.Reason != alarm.ChatReason {
		t.Fatalf("want auto chattering shelve, got %+v", sh)
	}
	if st != alarm.StateActiveUnacked {
		t.Fatalf("state machine still runs, got %v", st)
	}

	// 自动屏蔽期间触发不进入列表；到期 (12+5=17) 自动解除并出现。
	step(func() error { return s.Return(13, 9) })
	step(func() error { return s.Trigger(14, 9) })
	if list, _ := s.ActiveList(14); len(list) != 0 {
		t.Fatalf("auto-shelved must be hidden: %+v", list)
	}
	if list, _ := s.ActiveList(17); len(list) != 1 {
		t.Fatalf("must reappear at auto-shelve expiry: %+v", list)
	}
}

// 紧急报警即使震荡也不自动屏蔽。
func TestChatteringEmergencyExempt(t *testing.T) {
	cfg := testCfg()
	s := alarm.New(cfg, []alarm.PointConfig{{ID: 5, Priority: alarm.PriorityEmergency}}, testLogger(t))
	for ts := 0; ts < 10; ts++ {
		if err := s.Trigger(ts, 5); err != nil {
			t.Fatal(err)
		}
		if err := s.Return(ts, 5); err != nil {
			t.Fatal(err)
		}
		if err := s.Ack(ts, 5, alarm.RoleOperator, "op"); err != nil {
			t.Fatal(err)
		}
	}
	sh, _, _, _ := s.ShelvingOf(10, 5)
	if sh.Active {
		t.Fatalf("emergency must never auto-shelve: %+v", sh)
	}
}

// 停用后启用：停用期间触发/返回被忽略；启用后状态一律正常。
func TestDisableEnableInitialState(t *testing.T) {
	s := alarm.New(testCfg(), []alarm.PointConfig{{ID: 1, Priority: alarm.PriorityEmergency}}, testLogger(t))

	if err := s.Trigger(1, 1); err != nil {
		t.Fatal(err)
	}
	mustCode(t, s.Disable(2, 1, alarm.RoleOperator, "CHG-1"), alarm.ErrNoPermission)
	if err := s.Disable(2, 1, alarm.RoleEngineer, "CHG-1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ActiveList(2); len(list) != 0 {
		t.Fatalf("disabled alarm must leave list: %+v", list)
	}
	if err := s.Return(3, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Trigger(4, 1); err != nil {
		t.Fatal(err)
	}
	mustCode(t, s.Enable(5, 1, alarm.RoleEngineer, ""), alarm.ErrInvalidArg)
	if err := s.Enable(5, 1, alarm.RoleEngineer, "CHG-2"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ActiveList(5); len(list) != 0 {
		t.Fatalf("after enable must be normal until next trigger: %+v", list)
	}
	if err := s.Trigger(6, 1); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ActiveList(6); len(list) != 1 {
		t.Fatalf("next trigger after enable must activate: %+v", list)
	}
}
