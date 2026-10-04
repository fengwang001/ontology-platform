package replen

import (
	"errors"
	"testing"

	"ontology/task"
)

// 判定依据：每步操作后以 onHand/inTransit/urgent/reserve/avail/starved/任务清单全量断言。

// 以下方法白盒检视引擎内部增量状态与 touched 计数。

func (e *Engine) testOnHand(loc string) (int64, bool) {
	s, ok := e.world.Get(loc)
	if !ok {
		return 0, false
	}
	return s.OnHand, true
}

func (e *Engine) testInTransit(loc string) int64 {
	s, ok := e.world.Get(loc)
	if !ok {
		return -1
	}
	return s.InTransit
}

func (e *Engine) testUrgentQty(loc string) int64 {
	s, ok := e.world.Get(loc)
	if !ok {
		return -1
	}
	return s.UrgentQty
}

func (e *Engine) testReserve(sku string) int64 { return e.world.Reserve(sku) }

func (e *Engine) testAvail(sku string) int64 { return e.avail(sku) }

func (e *Engine) testStarved(loc string) int64 {
	s, ok := e.world.Get(loc)
	if !ok {
		return -1
	}
	return s.Starved
}

func (e *Engine) testTouched() int { return e.touched }

func (e *Engine) testTask(id int64) (*task.Task, bool) { return e.reg.Get(id) }

func (e *Engine) testSeq() int64 { return e.reg.Seq() }

func (e *Engine) openIDsAll() []int64 {
	ids := []int64{}
	for _, tk := range e.Tasks() {
		ids = append(ids, tk.ID)
	}
	return ids
}

type eng struct{ e *Engine }

func newEng() *eng { return &eng{e: New()} }

func (g *eng) mustOK(t *testing.T, name, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s | %s -> 非预期拒绝 %v", name, op, err)
	}
	t.Logf("%s | %s -> OK", name, op)
}

func (g *eng) mustErr(t *testing.T, name, op string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s | %s -> err=%v, 期望 errors.Is %v", name, op, err, want)
	}
	t.Logf("%s | %s -> 拒绝 %v（符合预期）", name, op, err)
}

func (g *eng) addSlot(t *testing.T, name, loc, sku string, min, max, cap, c, on int64) {
	g.mustOK(t, name, "AddSlot", g.e.AddSlot(loc, sku, min, max, cap, c, on))
}

func (g *eng) reserve(t *testing.T, name, sku string, qty int64) {
	g.mustOK(t, name, "AddReserve", g.e.AddReserve(sku, qty))
}

func (g *eng) pick(t *testing.T, name, loc string, qty int64) {
	g.mustOK(t, name, "Pick", g.e.Pick(loc, qty))
}

func (g *eng) demand(t *testing.T, name, loc string, need int64, wantUp, wantNew []int64) {
	t.Helper()
	up, created, err := g.e.Demand(loc, need)
	g.mustOK(t, name, "Demand", err)
	if !eqIDs(up, wantUp) || !eqIDs(created, wantNew) {
		t.Fatalf("%s | Demand(%s,%d) -> upgraded=%v new=%v, 期望 %v/%v",
			name, loc, need, up, created, wantUp, wantNew)
	}
	t.Logf("%s | Demand(%s,%d) -> upgraded=%v new=%v", name, loc, need, up, created)
}

func (g *eng) confirm(t *testing.T, name string, id, actual int64) {
	g.mustOK(t, name, "Confirm", g.e.Confirm(id, actual))
}

func (g *eng) cancel(t *testing.T, name string, id int64) {
	g.mustOK(t, name, "Cancel", g.e.Cancel(id))
}

func eqIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type state struct {
	onHand, inTransit, urgent, starved, reserve, avail int64
	tasks                                              []int64
}

func (g *eng) snapshot(loc, sku string) state {
	on, _ := g.e.testOnHand(loc)
	return state{
		onHand:    on,
		inTransit: g.e.testInTransit(loc),
		urgent:    g.e.testUrgentQty(loc),
		starved:   g.e.testStarved(loc),
		reserve:   g.e.testReserve(sku),
		avail:     g.e.testAvail(sku),
		tasks:     g.openIDs(),
	}
}

func (g *eng) openIDs() []int64 {
	ids := []int64{}
	for _, tk := range g.e.Tasks() {
		ids = append(ids, tk.ID)
	}
	return ids
}

func (g *eng) check(t *testing.T, name, loc, sku string, want state) {
	t.Helper()
	got := g.snapshot(loc, sku)
	fail := func(field string, gotv, wantv int64) {
		t.Fatalf("%s | %s 实际=%d 期望=%d", name, field, gotv, wantv)
	}
	if got.onHand != want.onHand {
		fail("onHand", got.onHand, want.onHand)
	}
	if got.inTransit != want.inTransit {
		fail("inTransit", got.inTransit, want.inTransit)
	}
	if got.urgent != want.urgent {
		fail("urgent", got.urgent, want.urgent)
	}
	if got.starved != want.starved {
		fail("starved", got.starved, want.starved)
	}
	if got.reserve != want.reserve {
		fail("reserve", got.reserve, want.reserve)
	}
	if got.avail != want.avail {
		fail("avail", got.avail, want.avail)
	}
	if !eqIDs(got.tasks, want.tasks) {
		t.Fatalf("%s | tasks=%v 期望=%v", name, got.tasks, want.tasks)
	}
	t.Logf("%s | 状态核对通过 on=%d transit=%d urgent=%d starved=%d reserve=%d avail=%d tasks=%v",
		name, got.onHand, got.inTransit, got.urgent, got.starved, got.reserve, got.avail, got.tasks)
}

func taskQty(e *Engine, id int64) int64 {
	tk, ok := e.testTask(id)
	if !ok {
		return -1
	}
	return tk.Qty
}

// TestWorkedExample1 题给第一例完整回放。
func TestWorkedExample1(t *testing.T) {
	g, n := newEng(), "ex1"
	g.addSlot(t, n, "K1", "S1", 10, 40, 60, 12, 15)
	g.reserve(t, n, "S1", 100)
	g.check(t, n, "K1", "S1", state{onHand: 15, reserve: 100, avail: 100, tasks: []int64{}})

	g.pick(t, n, "K1", 5)
	g.check(t, n, "K1", "S1", state{onHand: 10, inTransit: 24, reserve: 100, avail: 76, tasks: []int64{1}})
	if q := taskQty(g.e, 1); q != 24 {
		t.Fatalf("T1 qty=%d 期望 24", q)
	}

	g.pick(t, n, "K1", 8)
	g.check(t, n, "K1", "S1", state{onHand: 2, inTransit: 24, reserve: 100, avail: 76, tasks: []int64{1}})

	g.demand(t, n, "K1", 30, []int64{1}, []int64{2})
	g.check(t, n, "K1", "S1", state{onHand: 2, inTransit: 36, urgent: 36, reserve: 100, avail: 64, tasks: []int64{1, 2}})
	if q := taskQty(g.e, 2); q != 12 {
		t.Fatalf("T2 qty=%d 期望 12", q)
	}

	g.confirm(t, n, 1, 20)
	g.check(t, n, "K1", "S1", state{onHand: 22, inTransit: 12, urgent: 12, reserve: 76, avail: 64, tasks: []int64{2}})

	g.confirm(t, n, 2, 12)
	g.check(t, n, "K1", "S1", state{onHand: 34, reserve: 64, avail: 64, tasks: []int64{}})
}

// TestWorkedExample2 题给第二例各分支。
func TestWorkedExample2(t *testing.T) {
	// onHand=2、T1 常规 24；Demand 20：升级后 26≥20 即停，不新建。
	g, n := newEng(), "ex2a"
	g.addSlot(t, n, "K1", "S1", 10, 40, 60, 12, 15)
	g.reserve(t, n, "S1", 100)
	g.pick(t, n, "K1", 5) // on=10 -> T1 常规 24
	g.pick(t, n, "K1", 8) // on=2, eff=26 不再触发
	g.demand(t, n, "K1", 20, []int64{1}, []int64{})
	g.check(t, n, "K1", "S1", state{onHand: 2, inTransit: 24, urgent: 24, reserve: 100, avail: 76, tasks: []int64{1}})

	// onHand=11 无在途：11>10，不触发。
	g2 := newEng()
	g2.addSlot(t, "ex2b", "K1", "S1", 10, 40, 60, 12, 11)
	g2.check(t, "ex2b", "K1", "S1", state{onHand: 11, tasks: []int64{}})

	// 触发时 avail=20：want=24，实补 12。
	g3, n3 := newEng(), "ex2c"
	g3.addSlot(t, n3, "K1", "S1", 10, 40, 60, 12, 15)
	g3.reserve(t, n3, "S1", 20)
	g3.pick(t, n3, "K1", 5)
	g3.check(t, n3, "K1", "S1", state{onHand: 10, inTransit: 12, reserve: 20, avail: 8, tasks: []int64{1}})

	// 触发时 avail=11：qty=0，Starved+1，不建任务。
	g4, n4 := newEng(), "ex2d"
	g4.addSlot(t, n4, "K1", "S1", 10, 40, 60, 12, 15)
	g4.reserve(t, n4, "S1", 11)
	g4.pick(t, n4, "K1", 5)
	g4.check(t, n4, "K1", "S1", state{onHand: 10, starved: 1, reserve: 11, avail: 11, tasks: []int64{}})

	// want=0：min=10 max=20 c=12 on=10 -> floor(10/12)*12=0，不建任务也不计 Starved。
	g5, n5 := newEng(), "ex2e"
	g5.addSlot(t, n5, "K1", "S1", 10, 20, 60, 12, 10)
	g5.check(t, n5, "K1", "S1", state{onHand: 10, starved: 0, tasks: []int64{}})

	// Cancel(T1) 后立即连锁新建任务号更大的常规任务。
	g6, n6 := newEng(), "ex2f"
	g6.addSlot(t, n6, "K1", "S1", 10, 40, 60, 12, 15)
	g6.reserve(t, n6, "S1", 100)
	g6.pick(t, n6, "K1", 5)
	g6.cancel(t, n6, 1)
	g6.check(t, n6, "K1", "S1", state{onHand: 10, inTransit: 24, reserve: 100, avail: 76, tasks: []int64{2}})
}
