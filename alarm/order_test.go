package alarm_test

import (
	"sync"
	"testing"

	"ontology/alarm"
)

// 列表排序四层并列：优先级 > 未确认先于已确认 > 进入激活时刻 > 编号。
func TestActiveListOrdering(t *testing.T) {
	s := alarm.New(testCfg(), []alarm.PointConfig{
		{ID: 10, Priority: alarm.PriorityLow},
		{ID: 11, Priority: alarm.PriorityHigh},
		{ID: 12, Priority: alarm.PriorityEmergency},
		{ID: 13, Priority: alarm.PriorityHigh},
		{ID: 14, Priority: alarm.PriorityHigh},
		{ID: 15, Priority: alarm.PriorityHigh},
	}, testLogger(t))

	trig := func(id, ts int) {
		t.Helper()
		if err := s.Trigger(ts, id); err != nil {
			t.Fatal(err)
		}
	}
	trig(11, 1) // 高、未确认、t=1
	trig(15, 1) // 高、未确认、t=1（与 11 同层同时刻，编号决胜）
	trig(10, 1) // 低、未确认、t=1
	trig(14, 2) // 高、未确认、t=2，稍后确认以落到已确认层
	if err := s.Ack(3, 14, alarm.RoleOperator, "op"); err != nil {
		t.Fatal(err)
	}
	trig(13, 5) // 高、未确认、t=5
	trig(12, 9) // 紧急、未确认、t=9

	list, err := s.ActiveList(10)
	if err != nil {
		t.Fatal(err)
	}
	got := idsOf(list)
	want := []int{12, 11, 15, 13, 14, 10}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order got %v want %v", got, want)
		}
	}
}

// 报警率区间左开右闭，同一报警多次出现按多次计。
func TestAlarmRateBoundary(t *testing.T) {
	cfg := testCfg()
	cfg.ChatCount = 1000 // 关闭震荡
	s := alarm.New(cfg, []alarm.PointConfig{{ID: 1, Priority: alarm.PriorityHigh}}, testLogger(t))

	appear := func(ts int) {
		t.Helper()
		if err := s.Trigger(ts, 1); err != nil {
			t.Fatal(err)
		}
		// active-unacked -> return-unacked（仍在列表）-> ack -> normal（离开列表）。
		if err := s.Return(ts, 1); err != nil {
			t.Fatal(err)
		}
		if err := s.Ack(ts, 1, alarm.RoleOperator, "op"); err != nil {
			t.Fatal(err)
		}
	}
	appear(10)
	appear(20)

	n, err := s.AlarmRate(20, 10) // (10,20]：排除10、包含20 -> 1
	if err != nil || n != 1 {
		t.Fatalf("(10,20] want 1 got %d err=%v", n, err)
	}
	n, err = s.AlarmRate(20, 11) // (9,20]：10、20 -> 2
	if err != nil || n != 2 {
		t.Fatalf("(9,20] want 2 got %d err=%v", n, err)
	}
	n, err = s.AlarmRate(25, 5) // (20,25]：20 被左边界排除 -> 0
	if err != nil || n != 0 {
		t.Fatalf("(20,25] want 0 got %d err=%v", n, err)
	}
	_, err = s.AlarmRate(26, 0)
	mustCode(t, err, alarm.ErrInvalidArg)
}

// 时钟回退与不存在的点均不留痕，时钟不被推进。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := alarm.New(testCfg(), []alarm.PointConfig{{ID: 1, Priority: alarm.PriorityHigh}}, testLogger(t))
	if err := s.Trigger(10, 1); err != nil {
		t.Fatal(err)
	}
	mustCode(t, s.Trigger(9, 1), alarm.ErrClockRollback)
	mustCode(t, s.Trigger(11, 999), alarm.ErrNoSuchPoint)
	mustCode(t, s.Ack(11, 999, alarm.RoleOperator, "op"), alarm.ErrNoSuchPoint)

	if err := s.Return(10, 1); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ActiveList(10)
	if len(list) != 1 || list[0].State != alarm.StateReturnUnacked || list[0].LastActiveAt != 10 {
		t.Fatalf("rejected ops must leave no trace: %+v", list)
	}

	// 时长超限被拒绝：屏蔽不得生效。
	mustCode(t, s.Shelve(11, 1, alarm.RoleOperator, testCfg().HighShelveMaxSec+1, "x"), alarm.ErrShelveTooLong)
	sh, _, _, _ := s.ShelvingOf(11, 1)
	if sh.Active {
		t.Fatalf("rejected shelve must not take effect: %+v", sh)
	}
}

// 错误次序：参数非法 > 时钟回退 > 点不存在 > 无权限 > 状态不允许 > 时长超限。
func TestErrorOrdering(t *testing.T) {
	s := alarm.New(testCfg(), []alarm.PointConfig{
		{ID: 1, Priority: alarm.PriorityEmergency},
		{ID: 2, Priority: alarm.PriorityHigh},
	}, testLogger(t))
	if err := s.Trigger(5, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Trigger(5, 2); err != nil {
		t.Fatal(err)
	}

	// 参数非法（负时刻/空原因）压过一切。
	mustCode(t, s.Shelve(-1, 1, alarm.RoleOperator, 10, ""), alarm.ErrInvalidArg)
	// 时钟回退压过点不存在（操作点虽不存在，但时刻先回退）。
	mustCode(t, s.Shelve(1, 999, alarm.RoleOperator, 10, "x"), alarm.ErrClockRollback)
	// 时刻合法但点不存在，压过无权限/状态/时长。
	mustCode(t, s.Disable(6, 999, alarm.RoleOperator, ""), alarm.ErrInvalidArg) // 空单号仍是参数非法
	mustCode(t, s.Disable(6, 999, alarm.RoleOperator, "T"), alarm.ErrNoSuchPoint)
	// 点存在 + 时刻合法：无权限压过紧急不可屏蔽。
	mustCode(t, s.Disable(6, 1, alarm.RoleOperator, "T"), alarm.ErrNoPermission)
	// 有权限但状态不允许（紧急不可屏蔽）压过时长超限。
	mustCode(t, s.Shelve(6, 1, alarm.RoleOperator, 1<<30, "x"), alarm.ErrStateNotAllowed)
	// 状态允许时，超长才报时长超限。
	mustCode(t, s.Shelve(6, 2, alarm.RoleOperator, testCfg().HighShelveMaxSec+1, "x"), alarm.ErrShelveTooLong)
}

// 并发：所有调用在单一互斥下串行化，结果等价于某个串行顺序，无竞态/崩溃。
func TestConcurrentSerializability(t *testing.T) {
	s := alarm.New(testCfg(), []alarm.PointConfig{
		{ID: 1, Priority: alarm.PriorityHigh},
		{ID: 2, Priority: alarm.PriorityLow},
	}, testLogger(t))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ts := base*200 + i
				_ = s.Trigger(ts, 1)
				_, _ = s.ActiveList(ts)
				_, _ = s.AlarmRate(ts, 5)
			}
		}(g)
	}
	wg.Wait()
}
