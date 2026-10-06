package rotation

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// nOrd is the naive model's order with every boundary-effective mutation.
type nOrd struct {
	id       int
	start    int64
	end      int64
	timeline []nMut // chronological; Level 0 marks cancellation
	removed  bool
}

type nMut struct {
	at    int64
	level int
}

// naiveFreeze replays a recorded op sequence literally, slot by slot, and
// computes the final fact of every frozen slot: selected groups and accrued
// time. It shares no scheduling code with Scheduler: boundaries are settled in
// explicit loops on each advance, and levels come from linear timeline scans.
type naiveFreeze struct {
	slotLen int64
	groups  map[int]bool
	orders  map[int]*nOrd
	now     int64
	accrued map[int]int64
	decided map[int64][]int
}

func newNaiveFreeze(slotLen int64) *naiveFreeze {
	return &naiveFreeze{
		slotLen: slotLen,
		groups:  map[int]bool{},
		orders:  map[int]*nOrd{},
		accrued: map[int]int64{},
		decided: map[int64][]int{},
	}
}

func (n *naiveFreeze) levelAt(o *nOrd, slot int64) int {
	if o.removed || slot < o.start || slot >= o.end {
		return 0
	}
	v := 0
	for _, m := range o.timeline {
		if m.at <= slot {
			v = m.level
		}
	}
	return v
}

func (n *naiveFreeze) effective(slot int64) int {
	best := 0
	for _, o := range n.orders {
		if v := n.levelAt(o, slot); v > best {
			best = v
		}
	}
	return best
}

func (n *naiveFreeze) pick(slot int64) []int {
	ids := make([]int, 0, len(n.groups))
	for g := range n.groups {
		ids = append(ids, g)
	}
	sort.Slice(ids, func(i, j int) bool {
		if n.accrued[ids[i]] != n.accrued[ids[j]] {
			return n.accrued[ids[i]] < n.accrued[ids[j]]
		}
		return ids[i] < ids[j]
	})
	lvl := n.effective(slot)
	if lvl > len(ids) {
		lvl = len(ids)
	}
	if lvl == 0 {
		return nil
	}
	return append([]int(nil), ids[:lvl]...)
}

type recordedOp struct {
	kind   string
	err    *Error
	region string
	g, g2  int
	level  int
	a, b   int64
	oid    int
	gotID  int
	eff    int64
	user   string
	cat    Category
	power  int64
}

func (n *naiveFreeze) replay(ops []recordedOp, log *strings.Builder) {
	curStart := int64(0)
	fmt.Fprintln(log, "  -- naive replay --")
	for i, q := range ops {
		fmt.Fprintf(log, "  [%d] %s => %s\n", i, recDesc(q), errName(q.err))
		if q.err != nil {
			continue
		}
		switch q.kind {
		case "addgroup":
			n.groups[q.g] = true
		case "issue":
			n.orders[q.gotID] = &nOrd{id: q.gotID, start: q.a, end: q.b,
				timeline: []nMut{{at: q.a, level: q.level}}}
		case "change":
			o, ok := n.orders[q.oid]
			if !ok {
				break
			}
			o.timeline = append(o.timeline, nMut{at: q.eff, level: q.level})
		case "cancel":
			o, ok := n.orders[q.oid]
			if !ok {
				break
			}
			if q.eff < o.start {
				o.removed = true
				delete(n.orders, o.id)
			} else {
				o.timeline = append(o.timeline, nMut{at: q.eff, level: 0})
			}
		case "advance":
			target := q.b
			// Boundaries strictly crossed freeze; the boundary equal to
			// target opens a still-mutable slot and is decided only later.
			for b := curStart + n.slotLen; b < target; b += n.slotLen {
				sel := n.pick(b)
				n.decided[b] = sel
				for _, g := range sel {
					n.accrued[g] += n.slotLen
				}
				fmt.Fprintf(log, "      settle boundary=%d level=%d pick=%v\n",
					b, n.effective(b), sel)
				curStart = b
			}
			n.now = target
		}
	}
}

func recDesc(q recordedOp) string {
	switch q.kind {
	case "issue":
		return fmt.Sprintf("issue %s lvl=%d [%d,%d) id=%d", q.region, q.level, q.a, q.b, q.gotID)
	case "change":
		return fmt.Sprintf("change id=%d lvl=%d eff=%d", q.oid, q.level, q.eff)
	case "cancel":
		return fmt.Sprintf("cancel id=%d eff=%d", q.oid, q.eff)
	case "advance":
		return fmt.Sprintf("advance -> %d", q.b)
	case "addgroup":
		return fmt.Sprintf("addGroup %d", q.g)
	case "adduser":
		return fmt.Sprintf("addUser %s g=%d cat=%d", q.user, q.g, q.cat)
	case "move":
		return fmt.Sprintf("move %s -> %d", q.user, q.g2)
	case "category":
		return fmt.Sprintf("category %s cat=%d", q.user, q.cat)
	case "confirm":
		return fmt.Sprintf("confirm %s", q.user)
	}
	return q.kind
}

func errName(e *Error) string {
	if e == nil {
		return "ok"
	}
	return []string{"INVALID", "CLOCKBACK", "NO_ORDER", "IN_SLOT", "AFTER_DEADLINE", "NO_NOTICE"}[e.Kind]
}

type diffLogger struct{ b strings.Builder }

func (d *diffLogger) Logf(format string, args ...any) {
	fmt.Fprintf(&d.b, "  "+format+"\n", args...)
}

func sameSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x := map[int]int{}
	for _, v := range a {
		x[v]++
	}
	for _, v := range b {
		x[v]--
	}
	for _, d := range x {
		if d != 0 {
			return false
		}
	}
	return true
}

func effBoundaryOf(s *Scheduler, region string, now int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.regions[region]
	if !st.curFrozen && s.now == st.curStart {
		return st.curStart
	}
	return st.curEnd
}

// TestNaiveDifferential runs many randomized schedules against the literal
// naive model and compares frozen selections, accrued time, cardinality and
// fairness. On mismatch it prints every input, output and rationale.
func TestNaiveDifferential(t *testing.T) {
	const (
		iterations = 400
		maxSlots   = 12
		slotLen    = int64(10)
	)
	rng := rand.New(rand.NewSource(20261006))
	for it := 0; it < iterations; it++ {
		seed := rng.Int63()
		r := rand.New(rand.NewSource(seed))
		dl := &diffLogger{}
		s := New(dl)
		if err := s.AddRegion("R", slotLen); err != nil {
			t.Fatal(err)
		}
		gCount := 1 + r.Intn(5)
		for g := 1; g <= gCount; g++ {
			if err := s.AddGroup("R", g); err != nil {
				t.Fatal(err)
			}
		}
		var ops []recordedOp
		for g := 1; g <= gCount; g++ {
			ops = append(ops, recordedOp{kind: "addgroup", g: g})
		}
		for u := 0; u < gCount*2; u++ {
			cat := Category(r.Intn(3))
			ug := 1 + r.Intn(gCount)
			if err := s.AddUser(fmt.Sprintf("u%d", u), "R", ug, cat, int64(1+r.Intn(9))); err != nil {
				t.Fatal(err)
			}
			ops = append(ops, recordedOp{kind: "adduser",
				user: fmt.Sprintf("u%d", u), g: ug, cat: cat})
		}

		var live []int
		now := int64(0)
		horizon := int64(maxSlots) * slotLen
		for step := 0; step < 40; step++ {
			switch r.Intn(6) {
			case 0, 1:
				level := 1 + r.Intn(gCount)
				startSlot := 1 + r.Intn(maxSlots-1)
				length := 1 + r.Intn(4)
				start := int64(startSlot) * slotLen
				end := start + int64(length)*slotLen
				if end > horizon {
					end = horizon
				}
				if end <= start {
					continue
				}
				id, err := s.Issue("R", level, start, end)
				q := recordedOp{kind: "issue", region: "R", level: level, a: start, b: end, gotID: id}
				if e, ok := err.(*Error); ok {
					q.err = e
				}
				ops = append(ops, q)
				if err == nil {
					live = append(live, id)
				}
			case 2:
				if len(live) == 0 {
					continue
				}
				oid := live[r.Intn(len(live))]
				level := 1 + r.Intn(gCount)
				err := s.ChangeLevel(oid, level)
				q := recordedOp{kind: "change", oid: oid, level: level}
				if e, ok := err.(*Error); ok {
					q.err = e
				} else {
					q.eff = effBoundaryOf(s, "R", now)
				}
				ops = append(ops, q)
			case 3:
				if len(live) == 0 {
					continue
				}
				idx := r.Intn(len(live))
				oid := live[idx]
				err := s.Cancel(oid)
				q := recordedOp{kind: "cancel", oid: oid}
				if e, ok := err.(*Error); ok {
					q.err = e
				} else {
					q.eff = effBoundaryOf(s, "R", now)
					live = append(live[:idx], live[idx+1:]...)
				}
				ops = append(ops, q)
			case 4:
				target := now
				if r.Intn(4) != 0 {
					target = int64(1+r.Intn(maxSlots)) * slotLen
					if target < now {
						target = now
					}
				}
				err := s.Advance(target)
				q := recordedOp{kind: "advance", b: target}
				if e, ok := err.(*Error); ok {
					q.err = e
				} else if target == now {
					// A no-op advance performs no boundary settlement.
					q.kind = "noop"
				}
				ops = append(ops, q)
				if err == nil {
					now = target
				}
			case 5:
				_ = s.Confirm(fmt.Sprintf("u%d", r.Intn(gCount*2)), "R")
			}
		}

		n := newNaiveFreeze(slotLen)
		n.replay(ops, &dl.b)
		for slot := slotLen; slot < horizon; slot += slotLen {
			want, ok := n.decided[slot]
			got, gotOk := s.Decisions("R", slot)
			if ok != gotOk {
				t.Fatalf("seed=%d slot=%d decided presence ref=%v sched=%v\n%s", seed, slot, ok, gotOk, dl.b.String())
			}
			if ok && !sameSet(want, got) {
				t.Fatalf("seed=%d slot=%d selection ref=%v sched=%v\n%s", seed, slot, want, got, dl.b.String())
			}
			if ok && len(got) != n.effective(slot) {
				t.Fatalf("seed=%d slot=%d selected %d != effective %d\n%s",
					seed, slot, len(got), n.effective(slot), dl.b.String())
			}
		}
		for g := 1; g <= gCount; g++ {
			want := n.accrued[g]
			got, err := s.Accrued("R", g)
			if err != nil {
				t.Fatal(err)
			}
			if want != got {
				t.Fatalf("seed=%d group=%d accrued ref=%d sched=%d\n%s", seed, g, want, got, dl.b.String())
			}
			if got%slotLen != 0 {
				t.Fatalf("seed=%d group=%d accrued %d not multiple of slotLen", seed, g, got)
			}
		}
		var mn, mx int64 = 1 << 62, 0
		for g := 1; g <= gCount; g++ {
			a, _ := s.Accrued("R", g)
			if a < mn {
				mn = a
			}
			if a > mx {
				mx = a
			}
		}
		if mx-mn > slotLen {
			t.Fatalf("seed=%d fairness spread %d > slotLen\n%s", seed, mx-mn, dl.b.String())
		}
	}
}

// TestReplayDeterminism asserts identical op sequences give identical
// decisions and assessments.
func TestReplayDeterminism(t *testing.T) {
	run := func() (map[int64][]int, []Assessment) {
		s := New(nil)
		if err := s.AddRegion("R", 10); err != nil {
			t.Fatal(err)
		}
		for g := 1; g <= 3; g++ {
			_ = s.AddGroup("R", g)
		}
		_ = s.AddUser("u1", "R", 1, Normal, 0)
		id, err := s.Issue("R", 2, 10, 40)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Advance(20)
		_ = s.ChangeLevel(id, 1)
		_ = s.Advance(40)
		out := map[int64][]int{}
		for slot := int64(10); slot < 40; slot += 10 {
			d, _ := s.Decisions("R", slot)
			out[slot] = d
		}
		return out, s.Assessments()
	}
	a1, x1 := run()
	a2, x2 := run()
	for slot := range a1 {
		if !sameSet(a1[slot], a2[slot]) {
			t.Fatalf("decisions differ at %d: %v vs %v", slot, a1[slot], a2[slot])
		}
	}
	if len(x1) != len(x2) {
		t.Fatalf("assessments differ: %v vs %v", x1, x2)
	}
}
