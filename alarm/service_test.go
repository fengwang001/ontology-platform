package alarm_test

import (
	"log"
	"os"
	"testing"

	"ontology/alarm"
)

// testLogger 在 go test -v 下打印每个操作的输入、输出与判定依据。
func testLogger(t *testing.T) alarm.Logger {
	if testing.Verbose() {
		return log.New(os.Stdout, "    ", 0)
	}
	return nil
}

func testCfg() alarm.Config {
	return alarm.Config{
		ChatWindowSec:    10,
		ChatCount:        3,
		ChatShelveSec:    5,
		HighShelveMaxSec: 100,
		LowShelveMaxSec:  30,
	}
}

func mustCode(t *testing.T, err error, want alarm.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %v, got nil", want)
	}
	ae, ok := err.(*alarm.AlarmError)
	if !ok || ae.Code != want {
		t.Fatalf("want error code %v, got %v", want, err)
	}
}

func idsOf(list []alarm.ActiveAlarm) []int {
	out := make([]int, len(list))
	for i, a := range list {
		out[i] = a.ID
	}
	return out
}

// 基本状态机：正常->激活未确认->激活已确认->返回直接正常；
// 以及 激活未确认->返回未确认->确认->正常；重复触发为空操作。
func TestBasicTransitions(t *testing.T) {
	s := alarm.New(testCfg(), []alarm.PointConfig{{ID: 1, Priority: alarm.PriorityHigh}}, testLogger(t))

	if err := s.Trigger(1, 1); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ActiveList(1)
	if len(list) != 1 || list[0].State != alarm.StateActiveUnacked || list[0].LastActiveAt != 1 {
		t.Fatalf("after trigger: %+v", list)
	}

	// 重复触发为空操作：时刻不变、不计数。
	if err := s.Trigger(2, 1); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ActiveList(2)
	if list[0].State != alarm.StateActiveUnacked || list[0].LastActiveAt != 1 {
		t.Fatalf("repeat trigger must be no-op, got %+v", list)
	}

	if err := s.Ack(3, 1, alarm.RoleOperator, "op"); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ActiveList(3)
	if list[0].State != alarm.StateActiveAcked {
		t.Fatalf("want active-acked, got %v", list[0].State)
	}

	// 激活已确认后返回，直接正常（离开列表）。
	if err := s.Return(4, 1); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ActiveList(4)
	if len(list) != 0 {
		t.Fatalf("acked+return must be normal, got %+v", list)
	}

	// 正常确认属于状态不允许。
	mustCode(t, s.Ack(5, 1, alarm.RoleOperator, "op"), alarm.ErrStateNotAllowed)

	// 激活未确认 -> 返回未确认 -> 确认 -> 正常。
	s2 := alarm.New(testCfg(), []alarm.PointConfig{{ID: 7, Priority: alarm.PriorityLow}}, testLogger(t))
	if err := s2.Trigger(10, 7); err != nil {
		t.Fatal(err)
	}
	if err := s2.Return(11, 7); err != nil {
		t.Fatal(err)
	}
	list, _ = s2.ActiveList(11)
	if list[0].State != alarm.StateReturnUnacked {
		t.Fatalf("want return-unacked, got %v", list[0].State)
	}
	if err := s2.Ack(12, 7, alarm.RoleOperator, "op"); err != nil {
		t.Fatal(err)
	}
	list, _ = s2.ActiveList(12)
	if len(list) != 0 {
		t.Fatalf("ack of return-unacked must clear, got %+v", list)
	}
}

// 屏蔽期间返回再触发：屏蔽前已确认，期间返回后再次触发；
// 解除时必须以当前“激活未确认”出现，不得沿用屏蔽前的确认。
func TestShelveReturnRetrigger(t *testing.T) {
	s := alarm.New(testCfg(), []alarm.PointConfig{{ID: 2, Priority: alarm.PriorityHigh}}, testLogger(t))

	if err := s.Trigger(1, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(2, 2, alarm.RoleOperator, "op"); err != nil {
		t.Fatal(err)
	}
	if err := s.Shelve(3, 2, alarm.RoleOperator, 50, "maintenance"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ActiveList(3); len(list) != 0 {
		t.Fatalf("shelved alarm must be hidden: %+v", list)
	}

	// 屏蔽期间返回（激活已确认->正常）再触发（正常->激活未确认）。
	if err := s.Return(4, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Trigger(5, 2); err != nil {
		t.Fatal(err)
	}

	// 到期自动解除（until=53）：以激活未确认出现。
	list, err := s.ActiveList(53)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].State != alarm.StateActiveUnacked {
		t.Fatalf("must reappear active-unacked, got %+v", list)
	}

	// 报警率：t=53 这一次新出现应被计入 (53-10,53]。
	n, err := s.AlarmRate(53, 10)
	if err != nil || n != 1 {
		t.Fatalf("rate want 1 got %d err=%v", n, err)
	}
}

// 屏蔽与抑制重叠：两者都解除才出现；验证解除先后两种顺序。
func TestShelveSuppressOverlap(t *testing.T) {
	points := []alarm.PointConfig{{ID: 3, Priority: alarm.PriorityHigh, SuppressConditions: []string{"C"}}}

	// 顺序一：先解除屏蔽（仍抑制，不可见），后解除抑制（出现）。
	s := alarm.New(testCfg(), points, testLogger(t))
	if err := s.Trigger(1, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Shelve(2, 3, alarm.RoleOperator, 100, "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveConditions(3, []string{"C"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Unshelve(4, 3, alarm.RoleOperator); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ActiveList(4); len(list) != 0 {
		t.Fatalf("still suppressed, must stay hidden: %+v", list)
	}
	if err := s.SetActiveConditions(5, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ActiveList(5); len(list) != 1 {
		t.Fatalf("must appear after both lifted: %+v", list)
	}

	// 顺序二：先解除抑制（仍屏蔽，不可见），屏蔽到期后出现。
	s = alarm.New(testCfg(), points, testLogger(t))
	if err := s.Trigger(1, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveConditions(2, []string{"C"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shelve(3, 3, alarm.RoleOperator, 10, "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveConditions(4, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ActiveList(4); len(list) != 0 {
		t.Fatalf("still shelved, must stay hidden: %+v", list)
	}
	if list, _ := s.ActiveList(13); len(list) != 1 {
		t.Fatalf("must appear at shelve expiry: %+v", list)
	}
}
