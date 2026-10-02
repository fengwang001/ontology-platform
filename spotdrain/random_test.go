package spotdrain

// This file cross-checks Scheduler against a naive reference model written
// directly from the specification text. For 2000 random seeds it generates
// random nodes, tasks and event sequences, applies every operation to both
// implementations, and compares outputs and full state after each step,
// logging inputs, outputs and the judgment to each decision.

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// ----- naive reference model -----

type mTask struct {
	id              int64
	prio, w, iv, ck int64
	p, cp, rs       int64
	state           TaskState
	node            int64
}

type mNode struct {
	id      int64
	kind    NodeKind
	slots   int
	noticed bool
	dl      int64
	running map[int64]bool
}

type model struct {
	g, pod, bud int64
	nodes       map[int64]*mNode
	tasks       map[int64]*mTask
	rework      int64
	trace       []string // judgment basis for the last Notice
}

func newModel(g, pod, bud int64) *model {
	return &model{
		g: g, pod: pod, bud: bud,
		nodes: map[int64]*mNode{},
		tasks: map[int64]*mTask{},
	}
}

func (m *model) addNode(id int64, kind NodeKind, slots int) error {
	if id <= 0 || (kind != Spot && kind != OnDemand) || slots < 1 || slots > maxSlots {
		return ErrInvalidParam
	}
	if _, dup := m.nodes[id]; dup {
		return ErrNodeExists
	}
	m.nodes[id] = &mNode{id: id, kind: kind, slots: slots, running: map[int64]bool{}}
	return nil
}

func (m *model) addTask(id, prio, w, iv, ck int64) error {
	if id <= 0 || prio < 0 || prio > maxPrio ||
		w < 1 || w > maxWork || iv < 1 || iv > maxWork || ck < 1 || ck > maxWork {
		return ErrInvalidParam
	}
	if _, dup := m.tasks[id]; dup {
		return ErrTaskExists
	}
	m.tasks[id] = &mTask{id: id, prio: prio, w: w, iv: iv, ck: ck, state: Pending}
	return nil
}

func (m *model) place() {
	var pend []*mTask
	for _, t := range m.tasks {
		if t.state == Pending {
			pend = append(pend, t)
		}
	}
	sort.Slice(pend, func(i, j int) bool {
		if pend[i].prio != pend[j].prio {
			return pend[i].prio > pend[j].prio
		}
		return pend[i].id < pend[j].id
	})
	for _, t := range pend {
		placed := false
		if t.rs < 2 {
			var best *mNode
			bestFree := -1
			for _, n := range m.nodes {
				if n.kind != Spot || n.noticed {
					continue
				}
				free := n.slots - len(n.running)
				if free <= 0 {
					continue
				}
				if best == nil || free > bestFree || (free == bestFree && n.id < best.id) {
					best, bestFree = n, free
				}
			}
			if best != nil {
				best.running[t.id] = true
				t.state, t.node = Running, best.id
				placed = true
			}
		}
		if placed {
			continue
		}
		var best *mNode
		for _, n := range m.nodes {
			if n.kind != OnDemand || len(n.running) >= n.slots {
				continue
			}
			if best == nil || n.id < best.id {
				best = n
			}
		}
		if best == nil {
			continue
		}
		cost := (t.w - t.cp) * m.pod
		if cost > m.bud {
			continue // stays pending, does not block later tasks
		}
		m.bud -= cost
		best.running[t.id] = true
		t.state, t.node = Running, best.id
	}
}

func (m *model) report(id, p int64) error {
	t, ok := m.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if t.state != Running {
		return ErrTaskNotRunning
	}
	if p < t.p || p > t.w {
		return ErrInvalidProgress
	}
	t.p = p
	if cp := p / t.iv * t.iv; cp > t.cp {
		t.cp = cp
	}
	if p == t.w {
		delete(m.nodes[t.node].running, id)
		t.state, t.node = Completed, 0
	}
	return nil
}

func (m *model) notice(nid, now int64) ([]int64, []int64, []int64, error) {
	completed, saved, dropped := []int64{}, []int64{}, []int64{}
	m.trace = nil
	if now < 0 || now > maxNow {
		return completed, saved, dropped, ErrInvalidParam
	}
	n, ok := m.nodes[nid]
	if !ok {
		return completed, saved, dropped, ErrNodeNotFound
	}
	if n.kind != Spot {
		return completed, saved, dropped, ErrNotSpot
	}
	if n.noticed {
		return completed, saved, dropped, ErrAlreadyNoticed
	}
	n.noticed = true
	n.dl = now + m.g
	m.trace = append(m.trace, fmt.Sprintf("dl=%d", n.dl))

	type item struct{ id, u, ck int64 }
	var items []item
	ids := make([]int64, 0, len(n.running))
	for id := range n.running {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		t := m.tasks[id]
		switch rem := t.w - t.p; {
		case rem <= m.g:
			completed = append(completed, id)
			m.trace = append(m.trace,
				fmt.Sprintf("task %d: remaining %d <= G %d -> natural completion", id, rem, m.g))
		case t.p-t.cp == 0:
			saved = append(saved, id)
			m.trace = append(m.trace,
				fmt.Sprintf("task %d: u=0 -> saved (no time cost)", id))
		default:
			items = append(items, item{id: id, u: t.p - t.cp, ck: t.ck})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].u != items[j].u {
			return items[i].u > items[j].u
		}
		return items[i].id < items[j].id
	})
	var acc int64
	stopped := false
	for _, it := range items {
		if stopped {
			dropped = append(dropped, it.id)
			m.trace = append(m.trace,
				fmt.Sprintf("task %d: after stop -> dropped", it.id))
			continue
		}
		acc += it.ck
		if acc <= m.g {
			m.tasks[it.id].cp = m.tasks[it.id].p
			saved = append(saved, it.id)
			m.trace = append(m.trace,
				fmt.Sprintf("task %d: u=%d ck=%d cumulative=%d <= G %d -> saved, cp=p", it.id, it.u, it.ck, acc, m.g))
		} else {
			stopped = true
			dropped = append(dropped, it.id)
			m.trace = append(m.trace,
				fmt.Sprintf("task %d: u=%d ck=%d cumulative=%d > G %d -> dropped, stop", it.id, it.u, it.ck, acc, m.g))
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i] < completed[j] })
	sort.Slice(saved, func(i, j int) bool { return saved[i] < saved[j] })
	sort.Slice(dropped, func(i, j int) bool { return dropped[i] < dropped[j] })
	return completed, saved, dropped, nil
}

func (m *model) expire(nid, now int64) (map[int64]int64, error) {
	if now < 0 || now > maxNow {
		return nil, ErrInvalidParam
	}
	n, ok := m.nodes[nid]
	if !ok {
		return nil, ErrNodeNotFound
	}
	if !n.noticed {
		return nil, ErrNotNoticed
	}
	if now < n.dl {
		return nil, ErrNotExpired
	}
	rework := map[int64]int64{}
	for id := range n.running {
		t := m.tasks[id]
		rework[id] = t.p - t.cp
		m.rework += t.p - t.cp
		t.rs++
		t.p = t.cp
		t.state, t.node = Pending, 0
	}
	delete(m.nodes, nid)
	return rework, nil
}

// ----- randomized cross-check -----

type opKind int

const (
	opAddNode opKind = iota
	opAddTask
	opPlace
	opReport
	opNotice
	opExpire
)

type op struct {
	kind opKind
	args []int64
}

func (o op) String() string {
	name := [...]string{"AddNode", "AddTask", "Place", "Report", "Notice", "Expire"}[o.kind]
	return fmt.Sprintf("%s%v", name, o.args)
}

func genOps(rng *rand.Rand) (g, pod, bud int64, ops []op) {
	g = 1 + rng.Int63n(200)
	pod = 1 + rng.Int63n(10)
	bud = rng.Int63n(4000)
	n := 40 + rng.Intn(80)
	for i := 0; i < n; i++ {
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14:
			kind := Spot
			if rng.Intn(3) == 0 {
				kind = OnDemand
			}
			slots := int64(1 + rng.Intn(4))
			if rng.Intn(40) == 0 {
				slots = int64(rng.Intn(1200)) // sometimes invalid
			}
			ops = append(ops, op{opAddNode, []int64{int64(1 + rng.Intn(6)), int64(kind), slots}})
		case 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
			prio := rng.Int63n(6)
			if rng.Intn(40) == 0 {
				prio = rng.Int63n(300) // sometimes invalid
			}
			ops = append(ops, op{opAddTask, []int64{
				int64(1 + rng.Intn(10)), prio,
				1 + rng.Int63n(600), 1 + rng.Int63n(200), 1 + rng.Int63n(120),
			}})
		case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44:
			ops = append(ops, op{opPlace, nil})
		case 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64:
			ops = append(ops, op{opReport, []int64{int64(1 + rng.Intn(10)), rng.Int63n(700)}})
		case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74:
			ops = append(ops, op{opNotice, []int64{int64(1 + rng.Intn(6)), rng.Int63n(400)}})
		default:
			ops = append(ops, op{opExpire, []int64{int64(1 + rng.Intn(6)), rng.Int63n(400)}})
		}
	}
	return g, pod, bud, ops
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) || errors.Is(b, a)
}

// apply runs one op on both implementations and compares every output.
func apply(t *testing.T, s *Scheduler, m *model, o op) {
	t.Helper()
	switch o.kind {
	case opAddNode:
		errS := s.AddNode(o.args[0], NodeKind(o.args[1]), int(o.args[2]))
		errM := m.addNode(o.args[0], NodeKind(o.args[1]), int(o.args[2]))
		if !sameErr(errS, errM) {
			t.Fatalf("op %s: scheduler err %v, model err %v", o, errS, errM)
		}
		t.Logf("op %s -> err=%v", o, errS)
	case opAddTask:
		errS := s.AddTask(o.args[0], o.args[1], o.args[2], o.args[3], o.args[4])
		errM := m.addTask(o.args[0], o.args[1], o.args[2], o.args[3], o.args[4])
		if !sameErr(errS, errM) {
			t.Fatalf("op %s: scheduler err %v, model err %v", o, errS, errM)
		}
		t.Logf("op %s -> err=%v", o, errS)
	case opPlace:
		s.Place()
		m.place()
		t.Logf("op %s", o)
	case opReport:
		errS := s.Report(o.args[0], o.args[1])
		errM := m.report(o.args[0], o.args[1])
		if !sameErr(errS, errM) {
			t.Fatalf("op %s: scheduler err %v, model err %v", o, errS, errM)
		}
		t.Logf("op %s -> err=%v", o, errS)
	case opNotice:
		cS, sS, dS, errS := s.Notice(o.args[0], o.args[1])
		cM, sM, dM, errM := m.notice(o.args[0], o.args[1])
		if !sameErr(errS, errM) {
			t.Fatalf("op %s: scheduler err %v, model err %v", o, errS, errM)
		}
		if !reflect.DeepEqual(cS, cM) || !reflect.DeepEqual(sS, sM) || !reflect.DeepEqual(dS, dM) {
			t.Fatalf("op %s: scheduler (%v,%v,%v), model (%v,%v,%v)", o, cS, sS, dS, cM, sM, dM)
		}
		t.Logf("op %s -> completed=%v saved=%v dropped=%v err=%v", o, cS, sS, dS, errS)
		for _, line := range m.trace {
			t.Logf("  basis: %s", line)
		}
	case opExpire:
		rS, errS := s.Expire(o.args[0], o.args[1])
		rM, errM := m.expire(o.args[0], o.args[1])
		if !sameErr(errS, errM) {
			t.Fatalf("op %s: scheduler err %v, model err %v", o, errS, errM)
		}
		if !reflect.DeepEqual(rS, rM) {
			t.Fatalf("op %s: scheduler rework %v, model rework %v", o, rS, rM)
		}
		t.Logf("op %s -> rework=%v err=%v", o, rS, errS)
	}
}

// checkState compares the full visible state and verifies invariants.
func checkState(t *testing.T, s *Scheduler, m *model, prevBud int64) {
	t.Helper()
	if got := s.Budget(); got != m.bud {
		t.Fatalf("budget: scheduler %d, model %d", got, m.bud)
	}
	if s.Budget() < 0 || s.Budget() > prevBud {
		t.Fatalf("budget %d out of bounds (prev %d)", s.Budget(), prevBud)
	}
	if got := s.TotalRework(); got != m.rework {
		t.Fatalf("total rework: scheduler %d, model %d", got, m.rework)
	}
	for id := int64(1); id <= 10; id++ {
		info, okS := s.TaskInfo(id)
		mt, okM := m.tasks[id]
		if okS != okM {
			t.Fatalf("task %d: scheduler exists=%v, model exists=%v", id, okS, okM)
		}
		if !okS {
			continue
		}
		want := TaskInfo{State: mt.state, P: mt.p, Cp: mt.cp, Rs: mt.rs, NodeID: mt.node}
		if info != want {
			t.Fatalf("task %d: scheduler %+v, model %+v", id, info, want)
		}
		if info.P < info.Cp {
			t.Fatalf("task %d: p=%d < cp=%d", id, info.P, info.Cp)
		}
		if info.State == Running {
			nd, ok := m.nodes[info.NodeID]
			if !ok || !nd.running[id] {
				t.Fatalf("task %d running on unknown node %d", id, info.NodeID)
			}
			if nd.kind == Spot && info.Rs > 1 {
				t.Fatalf("task %d on spot with rs=%d", id, info.Rs)
			}
		}
	}
	for id := int64(1); id <= 6; id++ {
		info, okS := s.NodeInfo(id)
		mn, okM := m.nodes[id]
		if okS != okM {
			t.Fatalf("node %d: scheduler exists=%v, model exists=%v", id, okS, okM)
		}
		if !okS {
			continue
		}
		want := NodeInfo{Kind: mn.kind, Slots: mn.slots, Used: len(mn.running), Noticed: mn.noticed}
		if info != want {
			t.Fatalf("node %d: scheduler %+v, model %+v", id, info, want)
		}
		if info.Used > info.Slots {
			t.Fatalf("node %d: used %d > slots %d", id, info.Used, info.Slots)
		}
	}
}

func TestRandomAgainstModel(t *testing.T) {
	const groups = 2000
	for seed := int64(0); seed < groups; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			g, pod, bud, ops := genOps(rng)
			t.Logf("config: G=%d Pod=%d Bud=%d, %d ops", g, pod, bud, len(ops))
			s, err := NewScheduler(g, pod, bud)
			if err != nil {
				t.Fatalf("NewScheduler: %v", err)
			}
			m := newModel(g, pod, bud)
			prevBud := bud
			for _, o := range ops {
				apply(t, s, m, o)
				checkState(t, s, m, prevBud)
				prevBud = s.Budget()
			}
		})
	}
}

// TestReplayDeterminism: the same operation sequence replayed on a fresh
// scheduler yields identical drain plans, rework and placement results.
func TestReplayDeterminism(t *testing.T) {
	for seed := int64(1000); seed < 1050; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g, pod, bud, ops := genOps(rng)
		run := func() []string {
			s, err := NewScheduler(g, pod, bud)
			if err != nil {
				t.Fatalf("NewScheduler: %v", err)
			}
			var out []string
			for _, o := range ops {
				switch o.kind {
				case opAddNode:
					out = append(out, fmt.Sprint(s.AddNode(o.args[0], NodeKind(o.args[1]), int(o.args[2]))))
				case opAddTask:
					out = append(out, fmt.Sprint(s.AddTask(o.args[0], o.args[1], o.args[2], o.args[3], o.args[4])))
				case opPlace:
					s.Place()
				case opReport:
					out = append(out, fmt.Sprint(s.Report(o.args[0], o.args[1])))
				case opNotice:
					c, sv, d, err := s.Notice(o.args[0], o.args[1])
					out = append(out, fmt.Sprintf("%v %v %v %v", c, sv, d, err))
				case opExpire:
					r, err := s.Expire(o.args[0], o.args[1])
					out = append(out, fmt.Sprintf("%v %v", r, err))
				}
			}
			for id := int64(1); id <= 10; id++ {
				if info, ok := s.TaskInfo(id); ok {
					out = append(out, fmt.Sprintf("task %d %+v", id, info))
				}
			}
			out = append(out, fmt.Sprintf("bud=%d rework=%d", s.Budget(), s.TotalRework()))
			return out
		}
		first, second := run(), run()
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("seed %d: replay diverged", seed)
		}
	}
}
