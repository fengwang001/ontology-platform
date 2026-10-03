package sla

import "testing"

import "ontology/cal"

// TestLargeBudgetAndHolidays 验证大预算与多节假日时触发时刻仍可快速现算
// （prefix 为整周批量 + treap 修正，advance 为约 40 次二分，不逐日扫描）。
func TestLargeBudgetAndHolidays(t *testing.T) {
	for _, holidays := range []int{0, 100} {
		sm, c := setup(t)
		id := []byte("big")
		if err := sm.Start(id, 1_000_000_000, 0); err != nil {
			t.Fatal(err)
		}
		clock := int64(0)
		for d := 0; d < holidays; d++ {
			clock = int64(d+1) * 1440 * 3
			if err := c.AddHoliday(int64(100+d*7), clock); err != nil {
				t.Fatal(err)
			}
		}
		dl, ok, err := sm.Deadline(id, clock)
		if err != nil || !ok || dl > 1_000_000_000_000 {
			t.Fatalf("holidays=%d Deadline=(%d,%v,%v)", holidays, dl, ok, err)
		}
		if dl < clock {
			dl = clock
		}
		el, _ := sm.Elapsed(id, dl)
		if el < 1_000_000_000 {
			t.Fatalf("holidays=%d Elapsed(deadline)=%d", holidays, el)
		}
	}
}

// TestDeadlineOutOfRange 验证截止时刻超出 1e12 时视为未到期。
func TestDeadlineOutOfRange(t *testing.T) {
	c, err := cal.New(0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	sm := NewManager(c)
	id := []byte("z")
	if err := sm.Start(id, 1_000_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := sm.Deadline(id, 0); ok {
		t.Fatal("每日仅 1 工作分钟，预算需 1e9 天，超出时间轴，应未到期")
	}
	if _, due, _ := sm.Trigger(id, 1_000_000_000, 0); due {
		t.Fatal("触发时刻超出 1e12 应 due=false")
	}
}
