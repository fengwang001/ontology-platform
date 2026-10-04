package replen_test

import (
	"errors"
	"fmt"
	"sort"

	"ontology/replen"
	"ontology/task"
)

// naiveSlot 是朴素模拟：inTransit/avail 每次全量重算，不做任何增量维护。
type naiveSlot struct {
	sku              string
	min, max, cap, c int64
	onHand           int64
	starved          int64
}

type naiveTask struct {
	id       int64
	sku, loc string
	qty      int64
	urgent   bool
	status   int // 0 open 1 done 2 cancelled
}

type naive struct {
	slots   map[string]*naiveSlot
	reserve map[string]int64
	tasks   map[int64]*naiveTask
	seq     int64
	log     []string
}

func newNaive() *naive {
	return &naive{
		slots:   map[string]*naiveSlot{},
		reserve: map[string]int64{},
		tasks:   map[int64]*naiveTask{},
	}
}

func (n *naive) inTransit(loc string) int64 {
	var sum int64
	for _, t := range n.tasks {
		if t.loc == loc && t.status == 0 {
			sum += t.qty
		}
	}
	return sum
}

func (n *naive) skuUsed(sku string) int64 {
	var sum int64
	for _, t := range n.tasks {
		if t.sku == sku && t.status == 0 {
			sum += t.qty
		}
	}
	return sum
}

func (n *naive) avail(sku string) int64 { return n.reserve[sku] - n.skuUsed(sku) }

func floorC(v, c int64) int64 {
	if v <= 0 {
		return 0
	}
	return v / c * c
}

func ceilC(v, c int64) int64 {
	if v <= 0 {
		return 0
	}
	return (v + c - 1) / c * c
}

func (n *naive) create(loc, sku string, qty int64, urgent bool) int64 {
	n.seq++
	n.tasks[n.seq] = &naiveTask{id: n.seq, sku: sku, loc: loc, qty: qty, urgent: urgent}
	return n.seq
}

// regularCheck 朴素常规检查（全量重算 inTransit/avail）。
func (n *naive) regularCheck(loc string) {
	sl := n.slots[loc]
	eff := sl.onHand + n.inTransit(loc)
	if eff > sl.min {
		return
	}
	want := floorC(sl.max-eff, sl.c)
	if want == 0 {
		return
	}
	qty := want
	if a := floorC(n.avail(sl.sku), sl.c); a < qty {
		qty = a
	}
	if qty > 0 {
		n.create(loc, sl.sku, qty, false)
		return
	}
	sl.starved++
}

// op 描述一条操作与输入。
type op struct {
	kind             string
	loc, sku         string
	a, b, c, d, e, f int64
}

func (o op) String() string {
	switch o.kind {
	case "addslot":
		return fmt.Sprintf("AddSlot(%s,%s,min=%d,max=%d,cap=%d,c=%d,onHand=%d)",
			o.loc, o.sku, o.a, o.b, o.c, o.d, o.e)
	case "reserve":
		return fmt.Sprintf("AddReserve(%s,%d)", o.sku, o.a)
	case "pick":
		return fmt.Sprintf("Pick(%s,%d)", o.loc, o.a)
	case "demand":
		return fmt.Sprintf("Demand(%s,%d)", o.loc, o.a)
	case "confirm":
		return fmt.Sprintf("Confirm(#%d,%d)", o.a, o.b)
	case "cancel":
		return fmt.Sprintf("Cancel(#%d)", o.a)
	}
	return o.kind
}

// result 是两边统一表示的操作输出。
type result struct {
	err      string
	upgraded []int64
	created  int64 // 0 表示未新建
}

func (n *naive) run(o op) result {
	switch o.kind {
	case "addslot":
		if o.loc == "" || o.sku == "" || o.a < 1 || !(o.a < o.b) ||
			!(o.b <= o.c) || o.c > 1_000_000_000 ||
			o.d < 1 || o.d > 1_000_000 || o.e < 0 || o.e > o.c {
			return result{err: "invalid"}
		}
		if _, ok := n.slots[o.loc]; ok {
			return result{err: "conflict"}
		}
		n.slots[o.loc] = &naiveSlot{
			sku: o.sku, min: o.a, max: o.b, cap: o.c, c: o.d, onHand: o.e,
		}
		n.regularCheck(o.loc)
		return result{}
	case "reserve":
		if o.sku == "" || o.a < 1 || o.a > 1_000_000_000 {
			return result{err: "invalid"}
		}
		known := false
		for _, sl := range n.slots {
			if sl.sku == o.sku {
				known = true
			}
		}
		if !known {
			return result{err: "notfound"}
		}
		if n.reserve[o.sku] > 1_000_000_000_000-o.a {
			return result{err: "overqty"}
		}
		n.reserve[o.sku] += o.a
		return result{}
	case "pick":
		if o.loc == "" || o.a < 1 {
			return result{err: "invalid"}
		}
		sl := n.slots[o.loc]
		if sl == nil {
			return result{err: "notfound"}
		}
		if o.a > sl.onHand {
			return result{err: "shortpick"}
		}
		sl.onHand -= o.a
		n.regularCheck(o.loc)
		return result{}
	case "demand":
		if o.loc == "" || o.a < 1 {
			return result{err: "invalid"}
		}
		sl := n.slots[o.loc]
		if sl == nil {
			return result{err: "notfound"}
		}
		if o.a > sl.cap {
			return result{err: "invalid"}
		}
		var res result
		if sl.onHand >= o.a {
			return res
		}
		// 按 ID 升序升级常规，直到 onHand+紧急量 >= need。
		var ids []int64
		for id, t := range n.tasks {
			if t.loc == o.loc && t.status == 0 && !t.urgent {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		urgentQty := int64(0)
		for _, id := range ids {
			if sl.onHand+urgentQty >= o.a {
				break
			}
			t := n.tasks[id]
			t.urgent = true
			urgentQty += t.qty
			res.upgraded = append(res.upgraded, id)
		}
		eff := sl.onHand + n.inTransit(o.loc)
		if eff < o.a {
			want := ceilC(o.a-eff, sl.c)
			capRoom := floorC(sl.cap-eff, sl.c)
			availRoom := floorC(n.avail(sl.sku), sl.c)
			qty := want
			if capRoom < qty {
				qty = capRoom
			}
			if availRoom < qty {
				qty = availRoom
			}
			if qty > 0 {
				res.created = n.create(o.loc, sl.sku, qty, true)
			}
		}
		return res
	case "confirm":
		if o.a < 1 || o.b < 0 {
			return result{err: "invalid"}
		}
		t, ok := n.tasks[o.a]
		if !ok {
			return result{err: "notfound"}
		}
		if t.status != 0 {
			return result{err: "state"}
		}
		if o.b > t.qty {
			return result{err: "overqty"}
		}
		sl := n.slots[t.loc]
		sl.onHand += o.b
		n.reserve[t.sku] -= t.qty
		t.status = 1
		n.regularCheck(t.loc)
		return result{}
	case "cancel":
		if o.a < 1 {
			return result{err: "invalid"}
		}
		t, ok := n.tasks[o.a]
		if !ok {
			return result{err: "notfound"}
		}
		if t.status != 0 {
			return result{err: "state"}
		}
		t.status = 2
		n.regularCheck(t.loc)
		return result{}
	}
	return result{err: "unknown"}
}

// snapshot 为可对比的完整状态。
type snapshot struct {
	slots   string // loc -> onHand,inTransit,starved
	reserve string // sku -> reserve,avail
	tasks   string // 未完成: (id,loc,qty,urgent) 排序
	seq     int64
}

func (n *naive) snapshot() snapshot {
	s := snapshot{seq: n.seq}
	locs := make([]string, 0, len(n.slots))
	for loc := range n.slots {
		locs = append(locs, loc)
	}
	sort.Strings(locs)
	for _, loc := range locs {
		sl := n.slots[loc]
		s.slots += fmt.Sprintf("%s[oh=%d,it=%d,st=%d];",
			loc, sl.onHand, n.inTransit(loc), sl.starved)
	}
	skuSet := map[string]bool{}
	for sku := range n.reserve {
		skuSet[sku] = true
	}
	for _, sl := range n.slots {
		skuSet[sl.sku] = true
	}
	skus := make([]string, 0, len(skuSet))
	for sku := range skuSet {
		skus = append(skus, sku)
	}
	sort.Strings(skus)
	for _, sku := range skus {
		s.reserve += fmt.Sprintf("%s[r=%d,a=%d];",
			sku, n.reserve[sku], n.avail(sku))
	}
	var ids []int64
	for id, t := range n.tasks {
		if t.status == 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := n.tasks[ids[i]], n.tasks[ids[j]]
		if a.urgent != b.urgent {
			return a.urgent
		}
		return a.id < b.id
	})
	for _, id := range ids {
		t := n.tasks[id]
		s.tasks += fmt.Sprintf("(#%d %s %d u=%v);", t.id, t.loc, t.qty, t.urgent)
	}
	return s
}

func engineSnapshot(e *replen.Engine) snapshot {
	s := snapshot{}
	tasks := e.Tasks()
	for _, t := range tasks {
		s.tasks += fmt.Sprintf("(#%d %s %d u=%v);",
			t.ID, t.Loc, t.Qty, t.Kind == task.Urgent)
	}
	for _, sl := range e.Slots() {
		s.slots += fmt.Sprintf("%s[oh=%d,it=%d,st=%d];",
			sl.Loc, sl.OnHand, sl.InTransit, sl.Starved)
	}
	for _, r := range e.Reserves() {
		s.reserve += fmt.Sprintf("%s[r=%d,a=%d];", r.SKU, r.Reserve, r.Avail)
	}
	return s
}

func errLabel(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, replen.ErrInvalid):
		return "invalid"
	case errors.Is(err, replen.ErrNotFound):
		return "notfound"
	case errors.Is(err, replen.ErrState):
		return "state"
	case errors.Is(err, replen.ErrConflict):
		return "conflict"
	case errors.Is(err, replen.ErrShortPick):
		return "shortpick"
	case errors.Is(err, replen.ErrOverQty):
		return "overqty"
	}
	return err.Error()
}

func engineRun(e *replen.Engine, o op) result {
	switch o.kind {
	case "addslot":
		return result{err: errLabel(e.AddSlot(o.loc, o.sku, o.a, o.b, o.c, o.d, o.e))}
	case "reserve":
		return result{err: errLabel(e.AddReserve(o.sku, o.a))}
	case "pick":
		return result{err: errLabel(e.Pick(o.loc, o.a))}
	case "demand":
		r, err := e.Demand(o.loc, o.a)
		res := result{err: errLabel(err), upgraded: r.Upgraded}
		if r.Created != nil {
			res.created = r.Created.ID
		}
		return res
	case "confirm":
		return result{err: errLabel(e.Confirm(o.a, o.b))}
	case "cancel":
		return result{err: errLabel(e.Cancel(o.a))}
	}
	return result{err: "unknown"}
}

func sameResult(a, b result) bool {
	if a.err != b.err || a.created != b.created {
		return false
	}
	if fmt.Sprint(a.upgraded) != fmt.Sprint(b.upgraded) {
		return false
	}
	return true
}
