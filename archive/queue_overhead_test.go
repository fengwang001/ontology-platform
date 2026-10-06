package archive

import "testing"

// TestAssignCostIndependentOfHistory：
// 卷上制造大量“已完成”的历史预约（预约后取消），再让当前只有少量等待者，
// 归还时的分配扫描步数必须只取决于当前存活队列，而不随历史预约总数增长。
func TestAssignCostIndependentOfHistory(t *testing.T) {
	cfg := testConfig()
	s := NewService(cfg)
	s.AddUser("borrower", ClassTopSecret)
	const history = 500
	for i := 0; i < history; i++ {
		s.AddUser("h"+itoa(i), ClassTopSecret)
	}
	s.AddUser("w1", ClassTopSecret)
	s.AddUser("w2", ClassTopSecret)
	s.AddUser("frozen", ClassTopSecret)
	s.AddVolume("v", ClassPublic)
	if o := s.Borrow(0, "borrower", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	now := 1
	for i := 0; i < history; i++ {
		id := "h" + itoa(i)
		if o := s.Reserve(now, id, "v"); !o.OK {
			t.Fatalf("reserve %s: %s", id, o.Reason)
		}
		now++
		if o := s.CancelReservation(now, id, "v"); !o.OK {
			t.Fatalf("cancel %s: %s", id, o.Reason)
		}
		now++
	}
	// 队首放一个“暂停冻结”的预约者（扫描必须越过他），随后是 w1、w2。
	if o := s.Reserve(now, "frozen", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	if o := s.Reserve(now, "w1", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	if o := s.Reserve(now, "w2", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	if q := s.GetVolume("v").QueueUserIDs; len(q) != 3 {
		t.Fatalf("live queue should be 3, got %d", len(q))
	}
	// 用另一卷制造逾期使 frozen 进入暂停，其预约冻结保留队位。
	s.AddVolume("fv", ClassPublic)
	if o := s.Borrow(now, "frozen", "fv"); !o.OK {
		t.Fatal(o.Reason)
	}
	if o := s.Return(now+100, "frozen", "fv"); !o.OK {
		t.Fatal(o.Reason)
	}
	if s.GetUser("frozen").Status != UserSuspended {
		t.Fatal("frozen must be suspended")
	}
	now = now + 101
	if o := s.Return(now, "borrower", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	steps := s.ScanSteps("v")
	if steps != 2 {
		t.Fatalf("scan steps=%d, must be 2 (frozen skipped, then w1) regardless of history=%d",
			steps, history)
	}
	// w1 取卷、再归还；此时存活队列只有 w2，扫描步数应为 1。
	if o := s.Pickup(now, "w1", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	if o := s.Return(now+1, "w1", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	if steps := s.ScanSteps("v"); steps != 2 {
		t.Fatalf("scan steps=%d, want 2 (frozen still skipped, then w2)", steps)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
