package replen

import (
	"fmt"
	"math/rand"
	"testing"
)

const simSKU = "SKU"

type sop struct {
	kind                 int
	loc                  string
	min, max, cap, c, on int64
	qty, id, actual      int64
}

const (
	sAddSlot = iota
	sReserve
	sPick
	sDemand
	sConfirm
	sCancel
)

func (o sop) String() string {
	switch o.kind {
	case sAddSlot:
		return fmt.Sprintf("AddSlot(%s,%s,%d,%d,%d,%d,%d)", o.loc, simSKU, o.min, o.max, o.cap, o.c, o.on)
	case sReserve:
		return fmt.Sprintf("AddReserve(%s,%d)", simSKU, o.qty)
	case sPick:
		return fmt.Sprintf("Pick(%s,%d)", o.loc, o.qty)
	case sDemand:
		return fmt.Sprintf("Demand(%s,%d)", o.loc, o.qty)
	case sConfirm:
		return fmt.Sprintf("Confirm(%d,%d)", o.id, o.actual)
	default:
		return fmt.Sprintf("Cancel(%d)", o.id)
	}
}

func pickTaskID(r *rand.Rand, m *naive) int64 {
	ids := make([]int64, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 1 // 大概率不存在 -> NotFound
	}
	if r.Intn(5) == 0 {
		return int64(r.Intn(int(m.seq) + 3)) // 混入不存在的号
	}
	return ids[r.Intn(len(ids))]
}

func runSimOp(e *Engine, m *naive, o sop) (res, res) {
	switch o.kind {
	case sAddSlot:
		err := e.AddSlot(o.loc, simSKU, o.min, o.max, o.cap, o.c, o.on)
		return rerr(err), m.addSlot(o.loc, o.min, o.max, o.cap, o.c, o.on)
	case sReserve:
		return rerr(e.AddReserve(simSKU, o.qty)), m.reserveN(o.qty)
	case sPick:
		return rerr(e.Pick(o.loc, o.qty)), m.pick(o.loc, o.qty)
	case sDemand:
		up, nw, err := e.Demand(o.loc, o.qty)
		er := rerr(err)
		er.up, er.new = up, nw
		return er, m.demand(o.loc, o.qty)
	case sConfirm:
		return rerr(e.Confirm(o.id, o.actual)), m.finish(o.id, o.actual, false)
	default:
		return rerr(e.Cancel(o.id)), m.finish(o.id, 0, true)
	}
}

func sameRes(a, b res) bool {
	return a.err == b.err && eqIDs(a.up, b.up) && eqIDs(a.new, b.new)
}

func compareStates(t *testing.T, e *Engine, m *naive, locs []string, step int, o sop) {
	t.Helper()
	for _, loc := range locs {
		ms, ok := m.slots[loc]
		if !ok {
			continue
		}
		on, _ := e.testOnHand(loc)
		if on != ms.onHand {
			t.Fatalf("step %d %s onHand engine=%d naive=%d", step, o, on, ms.onHand)
		}
		if tr := e.testInTransit(loc); tr != m.transit(loc) {
			t.Fatalf("step %d %s inTransit engine=%d naive=%d", step, o, tr, m.transit(loc))
		}
		if uq := e.testUrgentQty(loc); uq != m.urgentQty(loc) {
			t.Fatalf("step %d %s urgentQty engine=%d naive=%d", step, o, uq, m.urgentQty(loc))
		}
		if st := e.testStarved(loc); st != ms.starved {
			t.Fatalf("step %d %s starved engine=%d naive=%d", step, o, st, ms.starved)
		}
		if on+e.testInTransit(loc) > ms.cap {
			t.Fatalf("step %d %s 不变量破坏 on+transit>cap", step, o)
		}
	}
	if rv := e.testReserve(simSKU); rv != m.reserve {
		t.Fatalf("step %d %s reserve engine=%d naive=%d", step, o, rv, m.reserve)
	}
	if av := e.testAvail(simSKU); av != m.avail() {
		t.Fatalf("step %d %s avail engine=%d naive=%d", step, o, av, m.avail())
	}
	if e.testAvail(simSKU) < 0 {
		t.Fatalf("step %d %s avail<0", step, o)
	}
	if e.testSeq() != m.seq {
		t.Fatalf("step %d %s seq engine=%d naive=%d", step, o, e.testSeq(), m.seq)
	}
	// 任务清单（顺序）对照
	engIDs := e.openIDsAll()
	navIDs := m.openOrder()
	if !eqIDs(engIDs, navIDs) {
		t.Fatalf("step %d %s tasks engine=%v naive=%v", step, o, engIDs, navIDs)
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		r := rand.New(rand.NewSource(int64(g + 1)))
		e := New()
		m := newNaive()
		nLocs := 2 + r.Intn(5)
		locs := make([]string, nLocs)
		for i := range locs {
			locs[i] = fmt.Sprintf("L%d", i)
		}
		verbose := g < 5
		if verbose {
			t.Logf("=== group %d seed=%d locs=%d ===", g, g+1, nLocs)
		}
		steps := 40 + r.Intn(80)
		for s := 0; s < steps; s++ {
			loc := locs[r.Intn(len(locs))]
			var o sop
			switch r.Intn(14) {
			case 0, 1, 2:
				c := int64(1 + r.Intn(12))
				capv := int64(20 + r.Intn(180))
				minv := int64(1 + r.Intn(15))
				maxv := minv + 1 + int64(r.Intn(40))
				if maxv > capv {
					maxv = capv
				}
				if maxv <= minv {
					minv, maxv = 1, 2
				}
				o = sop{kind: sAddSlot, loc: loc, min: minv, max: maxv, cap: capv, c: c,
					on: int64(r.Intn(int(capv) + 1))}
			case 3, 4:
				o = sop{kind: sReserve, qty: int64(1 + r.Intn(300))}
			case 5, 6, 7, 8:
				o = sop{kind: sPick, loc: loc, qty: int64(1 + r.Intn(80))}
			case 9, 10:
				capv := int64(200)
				if ms, ok := m.slots[loc]; ok {
					capv = ms.cap
				}
				o = sop{kind: sDemand, loc: loc, qty: int64(1 + r.Intn(int(capv)))}
			case 11, 12:
				id := pickTaskID(r, m)
				actual := int64(r.Intn(60))
				if tk := m.tasks[id]; tk != nil && r.Intn(4) != 0 {
					actual = int64(r.Intn(int(tk.qty) + 1)) // 多数落在合法范围
				}
				o = sop{kind: sConfirm, id: id, actual: actual}
			default:
				o = sop{kind: sCancel, id: pickTaskID(r, m)}
			}
			er, nr := runSimOp(e, m, o)
			if verbose {
				t.Logf("g%d step %d 输入=%s 输出引擎={err=%q up=%v new=%v} 朴素={err=%q up=%v new=%v} 判定=%s",
					g, s, o, er.err, er.up, er.new, nr.err, nr.up, nr.new, verdict(sameRes(er, nr)))
			}
			if !sameRes(er, nr) {
				t.Fatalf("group %d step %d %s 结果不一致: 引擎=%+v 朴素=%+v", g, s, o, er, nr)
			}
			compareStates(t, e, m, locs, s, o)
		}
	}
}

func verdict(ok bool) string {
	if ok {
		return "一致"
	}
	return "不一致"
}
