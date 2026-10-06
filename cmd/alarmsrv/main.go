// 命令行演示：用一串带时刻的事件展示报警生命周期服务的行为。
// 运行：go run ./cmd/alarmsrv
package main

import (
	"fmt"
	"log"
	"os"

	"ontology/alarm"
)

func main() {
	logger := log.New(os.Stdout, "", 0)
	cfg := alarm.Config{
		ChatWindowSec:    10,
		ChatCount:        3,
		ChatShelveSec:    4,
		HighShelveMaxSec: 100,
		LowShelveMaxSec:  30,
	}
	points := []alarm.PointConfig{
		{ID: 1, Priority: alarm.PriorityEmergency},
		{ID: 2, Priority: alarm.PriorityHigh, SuppressConditions: []string{"startup"}},
		{ID: 3, Priority: alarm.PriorityLow},
	}
	s := alarm.New(cfg, points, logger)

	check := func(desc string, err error) {
		if err != nil {
			fmt.Printf("  -> %s: ERROR %v\n", desc, err)
			return
		}
		fmt.Printf("  -> %s: ok\n", desc)
	}

	check("trigger emergency(1)", s.Trigger(1, 1))
	check("trigger high(1)", s.Trigger(1, 2))
	check("trigger low(2)", s.Trigger(2, 3))
	check("ack high(2)", s.Ack(2, 2, alarm.RoleOperator, "op-a"))
	check("shelve emergency (must fail)", s.Shelve(3, 1, alarm.RoleOperator, 10, "x"))
	check("shelve low 20s", s.Shelve(3, 3, alarm.RoleOperator, 20, "cleaning"))
	check("suppress high via startup cond", s.SetActiveConditions(4, []string{"startup"}))
	check("shelve high during suppress", s.Shelve(5, 2, alarm.RoleOperator, 10, "maint"))

	list, err := s.ActiveList(6)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("active list @t=6 (expect only emergency): %v\n", ids(list))

	check("lift startup condition @t=7 (high still shelved)", s.SetActiveConditions(7, nil))
	list, _ = s.ActiveList(7)
	fmt.Printf("active list @t=7: %v\n", ids(list))

	// high 的屏蔽在 t=15 到期；low 的屏蔽在 t=23 到期。
	list, _ = s.ActiveList(15)
	fmt.Printf("active list @t=15 (high reappears, still acked): %v\n", ids(list))

	n, _ := s.AlarmRate(15, 15)
	fmt.Printf("alarm rate (0,15] = %d\n", n)

	// 震荡：对 low 点在窗口内制造 3 次进入激活（屏蔽解除后）。
	list, _ = s.ActiveList(23)
	fmt.Printf("active list @t=23 (low shelve expired): %v\n", ids(list))
	// 屏蔽解除时 low 仍为激活未确认，先返回+确认使其回正常，再做 3 次“进入激活”。
	check("return low @24", s.Return(24, 3))
	check("ack low @24", s.Ack(24, 3, alarm.RoleOperator, "op"))
	for _, ts := range []int{25, 26, 27} {
		check(fmt.Sprintf("chat trigger low @%d", ts), s.Trigger(ts, 3))
		check(fmt.Sprintf("return+clear low @%d", ts), s.Return(ts, 3))
		if ts < 27 {
			check(fmt.Sprintf("ack low @%d", ts), s.Ack(ts, 3, alarm.RoleOperator, "op"))
		}
	}
	sh, st, _, _ := s.ShelvingOf(27, 3)
	fmt.Printf("low after 3rd entry @t=27: state=%s auto-shelved=%v reason=%q until=%d\n",
		st, sh.Active, sh.Reason, sh.Until)
}

func ids(list []alarm.ActiveAlarm) []int {
	out := make([]int, 0, len(list))
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}
