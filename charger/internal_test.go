package charger

import "testing"

func mkS(id int64, plug int64, cap, min int) *Session {
	return &Session{ID: id, PlugAt: plug, MinP: min, selfCap: cap, State: StateWaiting, active: true}
}

func TestWaterfillTable(t *testing.T) {
	cases := []struct {
		name   string
		caps   []int
		plugs  []int64
		budget int
		want   []int
	}{
		{"3台预算10", []int{6, 6, 6}, []int64{0, 1, 2}, 10, []int{4, 3, 3}},
		{"封顶+余量", []int{2, 4, 4}, []int64{0, 1, 2}, 8, []int{2, 3, 3}},
		{"全封顶", []int{1, 1}, []int64{0, 1}, 10, []int{1, 1}},
		{"余量次序不同上限", []int{3, 5, 5}, []int64{2, 0, 1}, 9, []int{3, 3, 3}},
		{"余量只给未封顶", []int{2, 5, 5}, []int64{0, 1, 2}, 8, []int{2, 3, 3}},
		{"余量插枪序", []int{5, 5, 5}, []int64{5, 2, 9}, 8, []int{3, 3, 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := make([]*Session, len(c.caps))
			for i := range c.caps {
				pool[i] = mkS(int64(i+1), c.plugs[i], c.caps[i], 0)
			}
			got := waterfill(pool, c.budget)
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("i=%d got=%d want=%d all=%v", i, got[i], c.want[i], got)
				}
			}
		})
	}
}

func TestAllocatePreferClassTwoCars(t *testing.T) {
	// 预算 6：selfCap 7(min3) 与 8(min1)。
	// 分水: [3,3]，两都达标（3>=min），无需挂起。
	a := mkS(1, 17, 7, 3)
	b := mkS(2, 17, 8, 1)
	a.State = StateWaiting
	b.State = StateWaiting
	allocate([]*Session{a, b}, 6)
	if a.Power != 3 || a.State != StateCharging {
		t.Fatalf("a=%+v", a)
	}
	if b.Power != 3 || b.State != StateCharging {
		t.Fatalf("b=%+v", b)
	}
}

func TestAllocateWithVictimRemainder(t *testing.T) {
	// 预算 5：三台 selfCap 7，min 分别 3,3,1。
	// 水填 [2,2,1]：前两台不足 3 -> 挑最晚插枪者挂起（二者皆等待，选 plugAt 大）。
	a := mkS(1, 0, 7, 3)
	b := mkS(2, 1, 7, 3)
	c := mkS(3, 2, 7, 1)
	allocate([]*Session{a, b, c}, 5)
	// 首轮 [2,2,1]：a,b 不足 min=3；二者皆原等待，选最晚插枪的 b 挂起。
	// 重分 a,c：份额 [3,2]，c 达标 min=1 -> a=3, b 等待, c=2。
	if b.State != StateWaiting || b.Power != 0 {
		t.Fatalf("b 应等待: %+v", b)
	}
	if a.Power != 3 || c.Power != 2 {
		t.Fatalf("a=%d c=%d", a.Power, c.Power)
	}
}

func mkInternalSession(id int64, port string, need, max, min int, prio Priority) *Session {
	return &Session{ID: id, PortID: port, PlugAt: 0, Need: need, MaxP: max, MinP: min,
		Prio: prio, State: StateWaiting, UnplugAt: -1, FullAt: -1,
		selfCap: max, active: true}
}

func addInternal(st *Station, ss ...*Session) {
	for _, s := range ss {
		st.sess[s.ID] = s
		st.portSess[s.PortID] = s
	}
}

func newInternalStation(t *testing.T, total int, ports []Port) *Station {
	t.Helper()
	st, err := NewStation(total, ports)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestClockFillMidAdvance(t *testing.T) {
	st := newInternalStation(t, 30, []Port{{ID: "A", Cap: 10}, {ID: "B", Cap: 10}, {ID: "C", Cap: 10}})
	a := mkInternalSession(1, "A", 20, 10, 0, Normal)
	b := mkInternalSession(2, "B", 30, 10, 0, Normal)
	c := mkInternalSession(3, "C", 1000, 10, 0, Normal)
	addInternal(st, a, b, c)
	st.reallocateLocked()
	ev := st.advanceLocked(5)
	st.reallocateLocked()
	if len(ev) != 2 || ev[0].SessionID != 1 || ev[0].At != 2 ||
		ev[1].SessionID != 2 || ev[1].At != 3 {
		t.Fatalf("events=%v", ev)
	}
	if a.Charged != 20 || b.Charged != 30 {
		t.Fatalf("a=%d b=%d", a.Charged, b.Charged)
	}
}

func TestClockPowerChangeAtFill(t *testing.T) {
	st := newInternalStation(t, 15, []Port{{ID: "A", Cap: 10}, {ID: "B", Cap: 10}})
	a := mkInternalSession(1, "A", 8, 10, 0, Normal)
	b := mkInternalSession(2, "B", 100, 10, 0, Normal)
	addInternal(st, a, b)
	st.reallocateLocked()
	if a.Power != 8 || b.Power != 7 {
		t.Fatalf("初始 %d %d", a.Power, b.Power)
	}
	st.advanceLocked(2)
	st.reallocateLocked()
	if a.Charged != 8 {
		t.Fatalf("A=%d", a.Charged)
	}
	if b.Charged != 17 {
		t.Fatalf("B=%d want 17 (7@t1 + 10@t2)", b.Charged)
	}
}

func TestClockTwoFillsSameSecond(t *testing.T) {
	st := newInternalStation(t, 30, []Port{{ID: "A", Cap: 10}, {ID: "B", Cap: 10}, {ID: "C", Cap: 10}})
	a := mkInternalSession(1, "A", 20, 10, 0, Normal)
	b := mkInternalSession(2, "B", 20, 10, 0, Normal)
	c := mkInternalSession(3, "C", 25, 10, 0, Normal)
	addInternal(st, a, b, c)
	st.reallocateLocked()
	ev := st.advanceLocked(3)
	st.reallocateLocked()
	want := []FillEvent{{SessionID: 1, At: 2}, {SessionID: 2, At: 2}, {SessionID: 3, At: 3}}
	if len(ev) != 3 {
		t.Fatalf("ev=%v", ev)
	}
	for i := range want {
		if ev[i] != want[i] {
			t.Fatalf("ev[%d]=%v want %v", i, ev[i], want[i])
		}
	}
	if c.Charged != 25 || c.State != StateFull {
		t.Fatalf("c=%d state=%v", c.Charged, c.State)
	}
}

func TestClockPowerChangeWithinSameSecondTwoFill(t *testing.T) {
	// 两辆车在同一秒充满，第三方在它们释放后的“下一秒”才用新功率。
	st := newInternalStation(t, 30, []Port{{ID: "A", Cap: 10}, {ID: "B", Cap: 10}, {ID: "C", Cap: 10}})
	a := mkInternalSession(1, "A", 10, 10, 0, Normal)
	b := mkInternalSession(2, "B", 10, 10, 0, Normal)
	c := mkInternalSession(3, "C", 1000, 10, 0, Normal)
	addInternal(st, a, b, c)
	st.reallocateLocked()
	ev := st.advanceLocked(2)
	st.reallocateLocked()
	// t1: A,B 满；C 第1秒 10。t2: C 独占30但自身上限10 -> 再 10。
	if len(ev) != 2 || ev[0].At != 1 || ev[1].At != 1 {
		t.Fatalf("ev=%v", ev)
	}
	if c.Charged != 20 {
		t.Fatalf("C=%d want 20", c.Charged)
	}
}
