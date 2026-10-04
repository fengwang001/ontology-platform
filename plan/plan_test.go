package plan

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func newTestScheduler(t *testing.T, f, t0 int, f0 string) *Scheduler {
	t.Helper()
	s, err := New(f, t0, f0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.SetChangeover("A", "B", 20); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChangeover("B", "A", 15); err != nil {
		t.Fatal(err)
	}
	return s
}

func mkWO(id, fam string, dur, due, ready int) WorkOrder {
	return WorkOrder{ID: id, Family: fam, Duration: dur, Due: due, Ready: ready}
}

func mustInsert(t *testing.T, s *Scheduler, w WorkOrder, strict bool, now int) InsertResult {
	t.Helper()
	r, err := s.Insert(w, strict, now)
	if err != nil {
		t.Fatalf("Insert %s: %v", w.ID, err)
	}
	return r
}

func findEntry(es []Entry, id string) Entry {
	for _, e := range es {
		if e.ID == id {
			return e
		}
	}
	return Entry{}
}

func idList(es []Entry) string {
	out := ""
	for i, e := range es {
		if i > 0 {
			out += ","
		}
		out += e.ID
	}
	return out
}

func assertPlanEqual(t *testing.T, s *Scheduler, want []Entry, msg string) {
	t.Helper()
	got := s.List()
	if len(got) != len(want) {
		t.Fatalf("%s: len %d != %d", msg, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: [%d] got %+v want %+v", msg, i, got[i], want[i])
		}
	}
}

// TestSpecExample1 题目第一例（now 均为 0，F=30）。
func TestSpecExample1(t *testing.T) {
	s := newTestScheduler(t, 30, 0, "A")

	r1 := mustInsert(t, s, mkWO("W1", "A", 50, 100, 0), false, 0)
	if r1 != (InsertResult{30, 80, false}) {
		t.Fatalf("W1 = %+v", r1)
	}
	r2 := mustInsert(t, s, mkWO("W2", "B", 40, 200, 0), false, 0)
	if r2 != (InsertResult{100, 140, false}) {
		t.Fatalf("W2 = %+v", r2)
	}

	r3 := mustInsert(t, s, mkWO("W3", "A", 30, 150, 0), true, 0)
	if r3 != (InsertResult{80, 110, false}) {
		t.Fatalf("W3 = %+v", r3)
	}
	es := s.List()
	if idList(es) != "W1,W3,W2" {
		t.Fatalf("seq = %s", idList(es))
	}
	if w2 := findEntry(es, "W2"); w2.Start != 130 || w2.End != 170 {
		t.Fatalf("W2 = (%d,%d), want (130,170)", w2.Start, w2.End)
	}

	before := s.List()

	_, err := s.Insert(mkWO("W4", "B", 40, 150, 0), true, 0)
	if !errors.Is(err, ErrDueConflict) {
		t.Fatalf("W4 err = %v, want ErrDueConflict", err)
	}
	assertPlanEqual(t, s, before, "rejected W4")

	_, err = s.Insert(mkWO("W5", "A", 35, 120, 0), true, 0)
	if !errors.Is(err, ErrDueConflict) {
		t.Fatalf("W5/35 err = %v, want ErrDueConflict", err)
	}
	assertPlanEqual(t, s, before, "rejected W5/35")

	r5 := mustInsert(t, s, mkWO("W5", "A", 30, 120, 0), true, 0)
	if r5 != (InsertResult{80, 110, false}) {
		t.Fatalf("W5/30 = %+v", r5)
	}
	es = s.List()
	if idList(es) != "W1,W5,W3,W2" {
		t.Fatalf("seq = %s", idList(es))
	}
	if w3 := findEntry(es, "W3"); w3.Start != 110 || w3.End != 140 {
		t.Errorf("W3 = (%d,%d), want (110,140)", w3.Start, w3.End)
	}
	if w2 := findEntry(es, "W2"); w2.Start != 160 || w2.End != 200 {
		t.Errorf("W2 = (%d,%d), want (160,200)", w2.Start, w2.End)
	}
}

// TestSpecExample2 题目第二例：冻结边界、已冻结移除、固定 earliest。
func TestSpecExample2(t *testing.T) {
	s := newTestScheduler(t, 30, 0, "A")
	mustInsert(t, s, mkWO("W1", "A", 50, 100, 0), false, 0)
	mustInsert(t, s, mkWO("W3", "A", 30, 150, 0), true, 0)
	mustInsert(t, s, mkWO("W2", "B", 40, 200, 0), false, 0)

	// now=50 边界 80：W1(start30) 冻结，W3(start80) 恰等不冻结。
	if err := s.Remove("W2", 50); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, s, mkWO("W2", "B", 40, 200, 0), false, 50)
	es := s.List()
	if !findEntry(es, "W1").Frozen {
		t.Fatal("W1 should be frozen at now=50")
	}
	if findEntry(es, "W3").Frozen {
		t.Fatal("W3 start==now+F must NOT be frozen")
	}

	// now=51 边界 81：W3(start80) 小 1，冻结。
	if err := s.Remove("W3", 51); !errors.Is(err, ErrFrozen) {
		t.Fatalf("Remove W3 err = %v, want ErrFrozen", err)
	}

	r6 := mustInsert(t, s, mkWO("W6", "B", 10, 400, 0), false, 200)
	if r6 != (InsertResult{230, 240, false}) {
		t.Fatalf("W6 = %+v, want (230,240,false)", r6)
	}

	// due=120 的新单只能排在冻结的 W3 之后（此时 W2 也已冻结），即 W6 之前。
	mustInsert(t, s, mkWO("W7", "A", 5, 120, 0), false, 200)
	if idList(s.List()) != "W1,W3,W2,W7,W6" {
		t.Fatalf("seq = %s", idList(s.List()))
	}
}

// TestFreezeStartEqualBoundary start 恰等 now+F 不冻结，小 1 冻结。
func TestFreezeStartEqualBoundary(t *testing.T) {
	s := newTestScheduler(t, 30, 0, "A")
	mustInsert(t, s, mkWO("U1", "A", 50, 1000, 0), false, 0) // 30-80
	mustInsert(t, s, mkWO("U2", "A", 1, 1000, 0), false, 0)  // 80-81

	// 用一次 now=50 的被接受移除推进冻结（边界80），随后在 now=50 重插。
	if err := s.Remove("U2", 50); err != nil {
		t.Fatal(err)
	}
	es := s.List()
	if !findEntry(es, "U1").Frozen {
		t.Fatal("U1 start30 < 80 should freeze")
	}
	mustInsert(t, s, mkWO("U2", "A", 1, 1000, 0), false, 50) // earliest=80, start=80
	u2 := findEntry(s.List(), "U2")
	if u2.Start != 80 {
		t.Fatalf("U2 start = %d, want 80", u2.Start)
	}
	if u2.Frozen {
		t.Fatal("start == now+F (80==80) must NOT freeze")
	}

	// now=51：边界81，U2 start80 小 1，冻结。
	mustInsert(t, s, mkWO("U3", "A", 1, 2000, 0), false, 51)
	if !findEntry(s.List(), "U2").Frozen {
		t.Fatal("start 80 < boundary 81 should freeze")
	}
}

// TestEqualDuePositioning due 相等时按接受序号排后。
func TestEqualDuePositioning(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")
	mustInsert(t, s, mkWO("Q1", "A", 10, 100, 0), false, 0)
	mustInsert(t, s, mkWO("Q2", "A", 10, 100, 0), false, 0)
	mustInsert(t, s, mkWO("Q3", "A", 10, 50, 0), false, 0)
	mustInsert(t, s, mkWO("Q4", "A", 10, 100, 0), false, 0)
	if idList(s.List()) != "Q3,Q1,Q2,Q4" {
		t.Fatalf("seq = %s", idList(s.List()))
	}
}

// TestEndEqualsDue end 恰等 due：strict 接受，Delayed=false。
func TestEndEqualsDue(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")
	r := mustInsert(t, s, mkWO("P1", "A", 50, 50, 0), true, 0)
	if r != (InsertResult{0, 50, false}) {
		t.Fatalf("P1 = %+v, want (0,50,false)", r)
	}
	// P2 50-80；插 P3 使其后单 end 恰等 due 也算按期。
	mustInsert(t, s, mkWO("P2", "A", 30, 80, 0), true, 0)
	mustInsert(t, s, mkWO("P3", "A", 10, 90, 0), true, 0) // 80-90
	if p2 := findEntry(s.List(), "P2"); p2.Start != 50 || p2.End != 80 {
		t.Fatalf("P2 = (%d,%d), want (50,80)", p2.Start, p2.End)
	}
}

// TestAlreadyLateNotConflict 插入前已延期的工单变得更晚不算新增延期。
func TestAlreadyLateNotConflict(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")
	mustInsert(t, s, mkWO("L1", "A", 100, 50, 0), false, 0) // 0-100 已延期
	r := mustInsert(t, s, mkWO("L0", "A", 10, 40, 0), true, 0)
	if r.Delayed {
		t.Fatal("L0 itself on time, Delayed should be false")
	}
	l1 := findEntry(s.List(), "L1")
	if l1.Start != 10 || l1.End != 110 {
		t.Fatalf("L1 = (%d,%d), want (10,110)", l1.Start, l1.End)
	}

	// 但如果把一个按期工单推迟成延期，strict 必须拒绝。
	s2 := newTestScheduler(t, 0, 0, "A")
	mustInsert(t, s2, mkWO("M1", "A", 50, 100, 0), false, 0) // 0-50 按期
	if _, err := s2.Insert(mkWO("M0", "A", 60, 60, 0), true, 0); !errors.Is(err, ErrDueConflict) {
		t.Fatalf("insert making on-time order late: err=%v, want ErrDueConflict", err)
	}
}

// TestNonStrictAlwaysAccepted 非严格插单自身超期也接受。
func TestNonStrictAlwaysAccepted(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")
	r := mustInsert(t, s, mkWO("D1", "A", 100, 50, 0), false, 0)
	if !r.Delayed || r.End != 100 {
		t.Fatalf("D1 = %+v, want delayed end=100", r)
	}
}

// TestEarliestFixedAtAccept earliest 固定于接受时刻，不随时钟浮动。
func TestEarliestFixedAtAccept(t *testing.T) {
	s := newTestScheduler(t, 100, 0, "A")
	mustInsert(t, s, mkWO("E1", "A", 10, 1000, 0), false, 0)   // earliest100: 100-110
	mustInsert(t, s, mkWO("E2", "A", 10, 1000, 0), false, 500) // earliest600: 600-610
	e2 := findEntry(s.List(), "E2")
	if e2.Start != 600 || e2.End != 610 {
		t.Fatalf("E2 = (%d,%d), want (600,610)", e2.Start, e2.End)
	}
	// 时钟推进到 900 插入 E3：earliest=max(50,1000)=1000；E1/E2 已冻结且时刻不变。
	e1 := findEntry(s.List(), "E1")
	mustInsert(t, s, mkWO("E3", "A", 10, 1000, 50), false, 900)
	e3 := findEntry(s.List(), "E3")
	if e3.Start != 1000 || e3.End != 1010 {
		t.Fatalf("E3 = (%d,%d), want (1000,1010)", e3.Start, e3.End)
	}
	if e1.Start != 100 || e1.End != 110 || !e1.Frozen {
		t.Fatalf("frozen E1 = %+v", e1)
	}
	if e2.Start != 600 || e2.End != 610 {
		t.Fatalf("frozen E2 drifted = (%d,%d)", e2.Start, e2.End)
	}
	// 时钟再推进到 2000，非冻结段新单插入与重算不改变 E3 的固定 earliest。
	mustInsert(t, s, mkWO("E4", "A", 1, 5000, 0), false, 2000)
	e3 = findEntry(s.List(), "E3")
	if e3.Start != 1000 {
		t.Fatalf("E3 start drifted to %d, earliest must be fixed", e3.Start)
	}

	// 移除非冻结工单后，其后工单仍按各自固定 earliest 重算。
	s2 := newTestScheduler(t, 0, 0, "A")
	mustInsert(t, s2, mkWO("G1", "A", 10, 1000, 0), false, 0)  // earliest0: 0-10
	mustInsert(t, s2, mkWO("G2", "A", 10, 1000, 0), false, 0)  // 10-20
	mustInsert(t, s2, mkWO("G3", "A", 10, 1000, 90), false, 0) // earliest90: 90-100
	if err := s2.Remove("G2", 0); err != nil {
		t.Fatal(err)
	}
	if g3 := findEntry(s2.List(), "G3"); g3.Start != 90 || g3.End != 100 {
		t.Fatalf("G3 after remove = (%d,%d), want (90,100)", g3.Start, g3.End)
	}
}

// TestRemoveRecalc 移除后前驱改变、换型改变，后续重算且受 earliest 限制。
func TestRemoveRecalc(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")
	mustInsert(t, s, mkWO("R1", "A", 10, 1000, 0), false, 0)   // 0-10
	mustInsert(t, s, mkWO("R2", "B", 10, 1000, 0), false, 0)   // A->B20: 30-40
	mustInsert(t, s, mkWO("R3", "A", 10, 1000, 0), false, 0)   // B->A15: 55-65
	mustInsert(t, s, mkWO("R4", "A", 10, 1000, 100), false, 0) // earliest100: 100-110

	if err := s.Remove("R2", 0); err != nil {
		t.Fatal(err)
	}
	es := s.List()
	if idList(es) != "R1,R3,R4" {
		t.Fatalf("seq = %s", idList(es))
	}
	r3 := findEntry(es, "R3") // R1(A)->R3(A) 无换型：10-20
	if r3.Start != 10 || r3.End != 20 {
		t.Fatalf("R3 = (%d,%d), want (10,20)", r3.Start, r3.End)
	}
	r4 := findEntry(es, "R4") // 受 earliest100 限制
	if r4.Start != 100 || r4.End != 110 {
		t.Fatalf("R4 = (%d,%d), want (100,110)", r4.Start, r4.End)
	}
	if n := s.recalcCount(); n != 2 {
		t.Fatalf("recalc = %d, want 2 (R3,R4 only)", n)
	}

	// 移除不存在。
	if err := s.Remove("nope", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing = %v, want ErrNotFound", err)
	}
}

// TestAsymmetricChangeover 换型方向不同时长不同。
func TestAsymmetricChangeover(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")                      // A->B 20, B->A 15
	mustInsert(t, s, mkWO("V1", "B", 10, 1000, 0), false, 0) // f0=A: 20-30
	mustInsert(t, s, mkWO("V2", "A", 10, 1000, 0), false, 0) // B->A15: 45-55
	mustInsert(t, s, mkWO("V3", "B", 10, 1000, 0), false, 0) // A->B20: 75-85
	es := s.List()
	want := []struct{ start, end int }{{20, 30}, {45, 55}, {75, 85}}
	for i, w := range want {
		if es[i].Start != w.start || es[i].End != w.end {
			t.Fatalf("%s = (%d,%d), want (%d,%d)", es[i].ID, es[i].Start, es[i].End, w.start, w.end)
		}
	}
}

// TestRejectOrdering 拒绝只报第一个错误。
func TestRejectOrdering(t *testing.T) {
	long := string(make([]byte, 33))
	for i := range long {
		longBytes := []byte(long)
		longBytes[i] = 'z'
		long = string(longBytes)
	}

	// Insert: 参数非法优先于时钟回退/重复。
	s := newTestScheduler(t, 10, 0, "A")
	if _, err := s.Insert(mkWO("", "A", 10, 0, 0), false, -1); !errors.Is(err, ErrArgument) {
		t.Fatalf("invalid vs rollback: %v", err)
	}
	if _, err := s.Insert(mkWO("X", "A", 0, 0, 0), false, 0); !errors.Is(err, ErrArgument) {
		t.Fatalf("dur=0: %v", err)
	}
	if _, err := s.Insert(mkWO("X", "A", 100001, 0, 0), false, 0); !errors.Is(err, ErrArgument) {
		t.Fatalf("dur too big: %v", err)
	}
	if _, err := s.Insert(mkWO("X", long, 10, 0, 0), false, 0); !errors.Is(err, ErrArgument) {
		t.Fatalf("family too long: %v", err)
	}

	mustInsert(t, s, mkWO("X", "A", 10, 100, 0), false, 5)
	// 时钟回退优先于重复。
	if _, err := s.Insert(mkWO("X", "A", 10, 100, 0), false, 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback vs duplicate: %v", err)
	}
	// 时钟正常但编号重复。
	if _, err := s.Insert(mkWO("X", "A", 10, 100, 0), false, 5); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}

	// Remove: 参数非法 > 回退 > 不存在 > 已冻结。
	if err := s.Remove("", 4); !errors.Is(err, ErrArgument) {
		t.Fatalf("remove invalid vs rollback: %v", err)
	}
	if err := s.Remove("ZZZ", 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("remove rollback vs notfound: %v", err)
	}
	if err := s.Remove("ZZZ", 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove notfound: %v", err)
	}

	// 已冻结优先于交期冲突（Insert 新编号不会命中已冻结；用 Remove 验证 frozen 次序）。
	// now=100：边界110，X start=max(5+10? earliest15) =15，冻结。
	if err := s.Remove("X", 100); !errors.Is(err, ErrFrozen) {
		t.Fatalf("remove frozen: %v", err)
	}

	// Get 不存在。
	if _, err := s.Get("ZZZ"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get notfound: %v", err)
	}
	if _, err := s.Get(""); !errors.Is(err, ErrArgument) {
		t.Fatalf("get invalid: %v", err)
	}
}

// TestSetChangeoverState 计划非空时 SetChangeover 报 ErrState；参数非法仍先报参数。
func TestSetChangeoverState(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")
	mustInsert(t, s, mkWO("X", "A", 10, 100, 0), false, 0)
	if err := s.SetChangeover("A", "B", 5); !errors.Is(err, ErrState) {
		t.Fatalf("setco non-empty: %v, want ErrState", err)
	}
	if err := s.SetChangeover("A", "A", 1); !errors.Is(err, ErrArgument) {
		t.Fatalf("setco bad arg on non-empty: %v, want ErrArgument", err)
	}

	s2, err := New(1_000_001, 0, "A")
	if !errors.Is(err, ErrArgument) || s2 != nil {
		t.Fatalf("New F out of range: %v", err)
	}
	if _, err := New(0, -1, "A"); !errors.Is(err, ErrArgument) {
		t.Fatalf("New t0 negative: %v", err)
	}
	if _, err := New(0, 0, ""); !errors.Is(err, ErrArgument) {
		t.Fatalf("New empty f0: %v", err)
	}
}

// TestRejectedInsertNoClock 被拒绝操作不推进时钟、不推进冻结。
func TestRejectedInsertNoClock(t *testing.T) {
	s := newTestScheduler(t, 30, 0, "A")
	mustInsert(t, s, mkWO("K1", "A", 50, 1000, 0), false, 0) // 30-80
	before := s.List()

	// now=51 严格插单失败：若错误推进冻结，下一次 now=50 将报回退。
	if _, err := s.Insert(mkWO("K2", "A", 5000, 100, 0), true, 51); !errors.Is(err, ErrDueConflict) {
		t.Fatalf("strict fail: %v", err)
	}
	assertPlanEqual(t, s, before, "rejected strict insert")

	// 时钟仍停留在 0：now=50 合法。
	mustInsert(t, s, mkWO("K3", "A", 1, 2000, 0), false, 50) // earliest80
	k3 := findEntry(s.List(), "K3")
	if k3.Start != 80 || k3.Frozen {
		t.Fatalf("K3 = %+v, want start80 non-frozen (boundary80)", k3)
	}

	// now=49 现在应报回退（上次接受是 50）。
	if _, err := s.Insert(mkWO("K4", "A", 1, 3000, 0), false, 49); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback after accepted now=50: %v", err)
	}
}

// TestRecalcBoundPrefixIndependent 重算量只与插入点之后工单数有关。
func TestRecalcBoundPrefixIndependent(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := newTestScheduler(t, 0, 0, "A")
		// n 张同族工单，dur 1，due 递增；全部 now=0 插入，earliest=0。
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("O%05d", i)
			mustInsert(t, s, mkWO(id, "A", 1, 1_000_000+i, 0), false, 0)
		}
		// now=50：全部冻结（start 0..n-1，边界50 冻结前50张），非冻结段从 O00050 起。
		// 插入 due=999_999 的新单，位置=50，重算 = 1（新单）+ (n-50) 其后工单。
		r := mustInsert(t, s, mkWO("ZNEW", "A", 1, 999_999, 0), false, 50)
		if r.Start != 50 {
			t.Fatalf("n=%d: new start = %d, want 50", n, r.Start)
		}
		wantRecalc := 1 + (n - 50)
		if got := s.recalcCount(); got != wantRecalc {
			t.Fatalf("n=%d: recalc = %d, want %d", n, got, wantRecalc)
		}
		// 冻结工单时刻封存。
		es := s.List()
		for i := 0; i < 50; i++ {
			if es[i].Start != i || es[i].End != i+1 || !es[i].Frozen {
				t.Fatalf("n=%d: frozen[%d] = (%d,%d) frozen=%v", n, i, es[i].Start, es[i].End, es[i].Frozen)
			}
		}
	}
}

// TestConcurrentSerializability 并发混合调用不损坏状态，时钟单调。
func TestConcurrentSerializability(t *testing.T) {
	s := newTestScheduler(t, 0, 0, "A")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				now := g*100000 + k
				id := fmt.Sprintf("G%dK%d", g, k)
				if r, err := s.Insert(mkWO(id, "A", 1, 1<<30, 0), false, now); err == nil {
					if r.End <= r.Start {
						t.Errorf("bad interval %+v", r)
					}
				}
				_, _ = s.Get(id)
				_ = s.List()
				_ = s.Remove(id, now+1)
			}
		}(g)
	}
	wg.Wait()

	// 最终状态：相邻约束与序列时刻一致。
	es := s.List()
	for i := 1; i < len(es); i++ {
		if es[i].Start < es[i-1].End {
			t.Fatalf("invariant broken at %d: %d < %d", i, es[i].Start, es[i-1].End)
		}
	}
}

// TestReplayDeterminism 相同操作序列重放得到相同计划。
func TestReplayDeterminism(t *testing.T) {
	play := func() []Entry {
		s := newTestScheduler(t, 30, 0, "A")
		mustInsert(t, s, mkWO("R1", "A", 50, 500, 0), false, 0)
		mustInsert(t, s, mkWO("R2", "B", 40, 300, 0), true, 10)
		if _, err := s.Insert(mkWO("R3", "A", 999, 10, 0), true, 20); !errors.Is(err, ErrDueConflict) {
			t.Fatal(err)
		}
		mustInsert(t, s, mkWO("R4", "A", 20, 600, 100), false, 40)
		if err := s.Remove("R1", 70); !errors.Is(err, ErrFrozen) {
			t.Fatalf("remove frozen R1: %v", err)
		}
		mustInsert(t, s, mkWO("R5", "B", 5, 400, 0), false, 90)
		return s.List()
	}
	first := play()
	second := play()
	if len(first) != len(second) {
		t.Fatalf("replay len %d != %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay differs at %d:\n%+v\n%+v", i, first[i], second[i])
		}
	}
}
