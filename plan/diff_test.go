package plan

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// nOrder 是朴素模拟中的工单。
type nOrder struct {
	id, fam         string
	dur, due, ready int
	seq, earliest   int
	start, end      int
}

// naiveSim 对每个被接受操作都"从头重排"复算全部时刻，作为差分基准。
type naiveSim struct {
	f, t0  int
	f0     string
	co     map[[2]string]int
	orders []*nOrder
	now    int
	hasNow bool
	frozen int
	seq    int
	recalc int
}

func newNaive(f, t0 int, f0 string, co map[[2]string]int) *naiveSim {
	return &naiveSim{f: f, t0: t0, f0: f0, co: co}
}

func (n *naiveSim) change(a, b string) int { return n.co[[2]string{a, b}] }

func (n *naiveSim) recalcFrom(k int) int {
	if k >= len(n.orders) {
		return 0
	}
	prevEnd, prevFam := n.t0, n.f0
	if k > 0 {
		p := n.orders[k-1]
		prevEnd, prevFam = p.end, p.fam
	}
	for i := k; i < len(n.orders); i++ {
		o := n.orders[i]
		st := prevEnd + n.change(prevFam, o.fam)
		if o.earliest > st {
			st = o.earliest
		}
		o.start, o.end = st, st+o.dur
		prevEnd, prevFam = o.end, o.fam
	}
	return len(n.orders) - k
}

func (n *naiveSim) advanceFreeze(now int) {
	boundary := now + n.f
	for n.frozen < len(n.orders) && n.orders[n.frozen].start < boundary {
		n.frozen++
	}
}

func (n *naiveSim) snapshot() []Entry {
	out := make([]Entry, len(n.orders))
	for i, o := range n.orders {
		out[i] = Entry{
			ID: o.id, Family: o.fam, Duration: o.dur, Due: o.due, Ready: o.ready,
			Start: o.start, End: o.end, Frozen: i < n.frozen,
		}
	}
	return out
}

func (n *naiveSim) insert(w WorkOrder, strict bool, now int) (InsertResult, error) {
	if !validWO(w) || now < 0 || now > maxNow {
		return InsertResult{}, ErrArgument
	}
	if n.hasNow && now < n.now {
		return InsertResult{}, ErrClockRollback
	}
	for _, o := range n.orders {
		if o.id == w.ID {
			return InsertResult{}, ErrDuplicate
		}
	}

	oldFrozen := n.frozen
	n.advanceFreeze(now)

	no := &nOrder{
		id: w.ID, fam: w.Family, dur: w.Duration, due: w.Due, ready: w.Ready,
		seq: n.seq + 1, earliest: now + n.f,
	}
	if w.Ready > no.earliest {
		no.earliest = w.Ready
	}

	pos := n.frozen
	for pos < len(n.orders) && n.orders[pos].due <= w.Due {
		pos++
	}

	old := n.orders
	trial := make([]*nOrder, 0, len(old)+1)
	for _, o := range old[:pos] {
		oc := *o
		trial = append(trial, &oc)
	}
	trial = append(trial, no)
	for _, o := range old[pos:] {
		oc := *o
		trial = append(trial, &oc)
	}
	n.orders = trial
	nc := n.recalcFrom(pos)

	if strict {
		if no.end > no.due {
			n.orders = old
			n.frozen = oldFrozen
			return InsertResult{}, ErrDueConflict
		}
		for i := pos + 1; i < len(n.orders); i++ {
			before := old[i-1]
			after := n.orders[i]
			if before.end <= before.due && after.end > after.due {
				n.orders = old
				n.frozen = oldFrozen
				return InsertResult{}, ErrDueConflict
			}
		}
	}

	n.now, n.hasNow, n.seq, n.recalc = now, true, no.seq, nc
	return InsertResult{Start: no.start, End: no.end, Delayed: no.end > no.due}, nil
}

func (n *naiveSim) remove(id string, now int) error {
	if !validName(id) || now < 0 || now > maxNow {
		return ErrArgument
	}
	if n.hasNow && now < n.now {
		return ErrClockRollback
	}
	idx := -1
	for i, o := range n.orders {
		if o.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	oldFrozen := n.frozen
	n.advanceFreeze(now)
	if idx < n.frozen {
		n.frozen = oldFrozen
		return ErrFrozen
	}
	n.orders = append(n.orders[:idx], n.orders[idx+1:]...)
	n.recalc = n.recalcFrom(idx)
	n.now, n.hasNow = now, true
	return nil
}

func errName(e error) string {
	if e == nil {
		return "nil"
	}
	return e.Error()
}

func sameErr(a, b error) bool {
	return errName(a) == errName(b)
}

// TestRandomDifferential 1500 组随机操作序列，与朴素从头重排模拟逐字段差分。
func TestRandomDifferential(t *testing.T) {
	const trials = 1500
	rng := rand.New(rand.NewSource(20261004))
	fams := []string{"A", "B", "C"}

	for iter := 0; iter < trials; iter++ {
		f := []int{0, 10, 30, 100}[rng.Intn(4)]
		t0 := rng.Intn(50)
		f0 := fams[rng.Intn(len(fams))]
		co := map[[2]string]int{}
		for _, a := range fams {
			for _, b := range fams {
				if a != b && rng.Intn(2) == 0 {
					co[[2]string{a, b}] = rng.Intn(25)
				}
			}
		}

		s, err := New(f, t0, f0)
		if err != nil {
			t.Fatalf("iter %d New: %v", iter, err)
		}
		for k, v := range co {
			if err := s.SetChangeover(k[0], k[1], v); err != nil {
				t.Fatalf("iter %d SetChangeover: %v", iter, err)
			}
		}
		nm := newNaive(f, t0, f0, co)

		nop := 30 + rng.Intn(60)
		now := 0
		var log []string
		failf := func(format string, args ...any) {
			t.Fatalf("iter %d trace:\n%s\n判定依据: %s", iter,
				strings.Join(log, "\n"), fmt.Sprintf(format, args...))
		}

		for step := 0; step < nop; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4: // Insert
				id := fmt.Sprintf("WO%d", rng.Intn(nop/2+2))
				w := WorkOrder{
					ID:       id,
					Family:   fams[rng.Intn(len(fams))],
					Duration: 1 + rng.Intn(80),
					Due:      rng.Intn(900),
					Ready:    rng.Intn(400),
				}
				if rng.Intn(15) == 0 {
					w.Duration = 0 // 偶发非法
				}
				strict := rng.Intn(2) == 0
				now += rng.Intn(20)
				if rng.Intn(12) == 0 && now >= 5 {
					now -= 5 // 偶发时钟回退尝试
				}
				r1, e1 := s.Insert(w, strict, now)
				r2, e2 := nm.insert(w, strict, now)
				log = append(log, fmt.Sprintf(
					"Insert(%s,fam=%s,dur=%d,due=%d,ready=%d,strict=%v,now=%d) -> real(%d-%d late=%v,%s) naive(%d-%d late=%v,%s)",
					w.ID, w.Family, w.Duration, w.Due, w.Ready, strict, now,
					r1.Start, r1.End, r1.Delayed, errName(e1),
					r2.Start, r2.End, r2.Delayed, errName(e2)))
				if !sameErr(e1, e2) {
					failf("Insert 错误不一致 real=%v naive=%v", e1, e2)
				}
				if e1 == nil {
					if r1 != r2 {
						failf("Insert 结果不一致 real=%+v naive=%+v", r1, r2)
					}
					if s.recalcCount() != nm.recalc {
						failf("recalc 不一致 real=%d naive=%d", s.recalcCount(), nm.recalc)
					}
				}
			case 5, 6: // Remove
				id := fmt.Sprintf("WO%d", rng.Intn(nop/2+4))
				now += rng.Intn(20)
				e1 := s.Remove(id, now)
				e2 := nm.remove(id, now)
				log = append(log, fmt.Sprintf("Remove(%s,now=%d) -> real=%s naive=%s",
					id, now, errName(e1), errName(e2)))
				if !sameErr(e1, e2) {
					failf("Remove 错误不一致 real=%v naive=%v", e1, e2)
				}
				if e1 == nil && s.recalcCount() != nm.recalc {
					failf("Remove 后 recalc real=%d naive=%d", s.recalcCount(), nm.recalc)
				}
			case 7, 8: // Get / List
				if rng.Intn(2) == 0 {
					id := fmt.Sprintf("WO%d", rng.Intn(nop/2+4))
					g1, e1 := s.Get(id)
					var g2 Entry
					var e2 error
					var found bool
					for _, e := range nm.snapshot() {
						if e.ID == id {
							g2, found = e, true
						}
					}
					if !found {
						e2 = ErrNotFound
					}
					log = append(log, fmt.Sprintf("Get(%s) -> real(%d-%d frozen=%v,%s) naive(%d-%d frozen=%v,%s)",
						id, g1.Start, g1.End, g1.Frozen, errName(e1),
						g2.Start, g2.End, g2.Frozen, errName(e2)))
					if !sameErr(e1, e2) || (e1 == nil && g1 != g2) {
						failf("Get 不一致 real=(%+v,%v) naive=(%+v,%v)", g1, e1, g2, e2)
					}
				} else {
					log = append(log, fmt.Sprintf("List() -> %d 张", len(s.List())))
				}
			case 9: // SetChangeover
				a, b := fams[rng.Intn(3)], fams[rng.Intn(3)]
				minutes := rng.Intn(30)
				if rng.Intn(10) == 0 {
					minutes = 100001
				}
				e1 := s.SetChangeover(a, b, minutes)
				var e2 error
				if minutes > 100000 || (a == b && minutes != 0) {
					e2 = ErrArgument
				} else if len(nm.orders) > 0 {
					e2 = ErrState
				} else {
					co[[2]string{a, b}] = minutes
				}
				log = append(log, fmt.Sprintf("SetChangeover(%s,%s,%d) -> real=%s naive=%s",
					a, b, minutes, errName(e1), errName(e2)))
				if !sameErr(e1, e2) {
					failf("SetChangeover 不一致 real=%v naive=%v", e1, e2)
				}
			}

			// 每步：与朴素计划逐字段一致 + 结构不变量。
			l1, l2 := s.List(), nm.snapshot()
			if len(l1) != len(l2) {
				failf("step %d 长度不一致 %d != %d", step, len(l1), len(l2))
			}
			for i := range l1 {
				if l1[i] != l2[i] {
					failf("step %d plan[%d] real=%+v naive=%+v", step, i, l1[i], l2[i])
				}
			}
			nf := 0
			for _, e := range l1 {
				if e.Frozen {
					nf++
				}
			}
			for i := 1; i < len(l1); i++ {
				// 相邻约束 start_i >= end_{i-1} + C。
				if gap := l1[i].Start - l1[i-1].End - s.mt.Changeover(l1[i-1].Family, l1[i].Family); gap < 0 {
					failf("step %d 相邻约束破坏 [%d] gap=%d", step, i, gap)
				}
				// 非冻结段 due 升序（接受序号由位置稳定性保证，朴素差分已覆盖）。
				if i > nf && l1[i-1].Due > l1[i].Due {
					failf("step %d 非冻结段 due 乱序 [%d] %d>%d", step, i, l1[i-1].Due, l1[i].Due)
				}
			}
			// recalc 上界：不超过非冻结段工单数，与冻结前缀长度无关。
			if got := s.recalcCount(); got > len(l1)-nf {
				failf("step %d recalc=%d 超过非冻结段 %d", step, got, len(l1)-nf)
			}
		}
	}
}
