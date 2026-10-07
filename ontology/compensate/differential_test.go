package compensate

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// genAction builds a random action over objects o0..nObj-1.
func genAction(rng *rand.Rand, nObj int, name string) Action {
	n := 1 + rng.Intn(5)
	ops := make([]SubOp, 0, n)
	for i := 0; i < n; i++ {
		src := InstanceID(fmt.Sprintf("o%d", rng.Intn(nObj)))
		switch rng.Intn(4) {
		case 0, 1:
			ops = append(ops, SubOp{Kind: OpSetAttrs, Object: src,
				Attrs: Attrs{"v": rng.Intn(1000)}})
		case 2:
			dst := InstanceID(fmt.Sprintf("o%d", rng.Intn(nObj)))
			ops = append(ops, SubOp{Kind: OpCreateLink,
				Link: Link{ID: LinkID(fmt.Sprintf("%s-L%d", name, i)), Source: src, Target: dst}})
		default:
			ops = append(ops, SubOp{Kind: OpValidate, Object: src})
		}
	}
	plan := &FaultPlan{Apply: map[int]FaultPoint{}, Undo: map[int]FaultPoint{}}
	if rng.Intn(100) < 45 {
		plan.Apply[rng.Intn(n)] = []FaultPoint{FaultApplyFail, FaultApplyPanic}[rng.Intn(2)]
	}
	for i := 0; i < n; i++ {
		if _, isApply := plan.Apply[i]; isApply {
			continue
		}
		if rng.Intn(100) < 12 {
			plan.Undo[i] = []FaultPoint{FaultUndoFail, FaultUndoPanic}[rng.Intn(2)]
		}
	}
	return Action{Name: name, Ops: ops, Inject: plan}
}

func engineWorld(g *Graph) (map[InstanceID]Attrs, map[InstanceID]int64,
	map[LinkID]bool, map[InstanceID]int) {
	g.lock()
	defer g.unlock()
	attrs := map[InstanceID]Attrs{}
	for id, o := range g.objs {
		cp := make(Attrs, len(o.Attrs))
		for k, v := range o.Attrs {
			cp[k] = v
		}
		attrs[id] = cp
	}
	vers := map[InstanceID]int64{}
	for id, o := range g.objs {
		vers[id] = o.Version
	}
	links := map[LinkID]bool{}
	for id := range g.links {
		links[id] = true
	}
	pol := map[InstanceID]int{}
	for id, idx := range g.polluted {
		pol[id] = idx
	}
	return attrs, vers, links, pol
}

func sameWorld(t *testing.T, tag string,
	ea, na map[InstanceID]Attrs,
	ev, nv map[InstanceID]int64,
	el, nl map[LinkID]bool,
	ep, np map[InstanceID]int) {
	t.Helper()
	if fmt.Sprint(ea) != fmt.Sprint(na) {
		t.Fatalf("%s: attrs diverge\nengine=%v\nnaive =%v", tag, ea, na)
	}
	if fmt.Sprint(ev) != fmt.Sprint(nv) {
		t.Fatalf("%s: versions diverge\nengine=%v\nnaive =%v", tag, ev, nv)
	}
	if fmt.Sprint(el) != fmt.Sprint(nl) {
		t.Fatalf("%s: links diverge\nengine=%v\nnaive =%v", tag, el, nl)
	}
	if fmt.Sprint(ep) != fmt.Sprint(np) {
		t.Fatalf("%s: pollution diverge\nengine=%v\nnaive =%v", tag, ep, np)
	}
}

func printAction(b *strings.Builder, a Action) {
	fmt.Fprintf(b, "ACTION %s ops=%d\n", a.Name, len(a.Ops))
	for i, op := range a.Ops {
		marker := ""
		if a.Inject != nil {
			if fp, ok := a.Inject.Apply[i]; ok {
				marker += fmt.Sprintf(" applyFault=%d", fp)
			}
			if fp, ok := a.Inject.Undo[i]; ok {
				marker += fmt.Sprintf(" undoFault=%d", fp)
			}
		}
		switch op.Kind {
		case OpSetAttrs:
			fmt.Fprintf(b, "  #%d set_attrs %s %v%s\n", i, op.Object, op.Attrs, marker)
		case OpCreateLink:
			fmt.Fprintf(b, "  #%d create_link %s %s->%s%s\n", i, op.Link.ID,
				op.Link.Source, op.Link.Target, marker)
		case OpDeleteLink:
			fmt.Fprintf(b, "  #%d delete_link %s%s\n", i, op.Link.ID, marker)
		case OpValidate:
			fmt.Fprintf(b, "  #%d validate %s%s\n", i, op.Object, marker)
		}
	}
}

func printTrace(b *strings.Builder, events []TraceEvent) {
	for _, ev := range events {
		fmt.Fprintf(b, "    [%s] %s op#%d %s %s\n",
			ev.Phase, ev.Kind, ev.OpIndex, ev.Outcome, ev.Detail)
	}
}

// TestDifferentialSequential replays identical random sequences on the Engine
// and the naive oracle, comparing verdicts and observable world each step.
func TestDifferentialSequential(t *testing.T) {
	nObj := 5
	for _, seed := range []int64{1, 2, 7, 42, 99, 1680} {
		rng := rand.New(rand.NewSource(seed))
		g := NewGraphWith(nObj)
		m := NewNaiveModel(g)
		var log strings.Builder

		for step := 0; step < 60; step++ {
			a := genAction(rng, nObj, fmt.Sprintf("seed%d-step%d", seed, step))
			fmt.Fprintf(&log, "=== %s ===\n", a.Name)
			printAction(&log, a)

			tr := &SliceTracer{}
			r := NewEngine(g).WithTracer(tr).Execute(a)
			printTrace(&log, tr.Events)
			fmt.Fprintf(&log, "  ENGINE verdict: commit=%v primary=%s failedOp=%d compFails=%d\n",
				r.Committed, r.Primary, r.FailedOpIndex, len(r.CompFailures))

			o := m.Run(a, nil)
			fmt.Fprintf(&log, "  NAIVE  verdict: commit=%v primary=%s failedOp=%d compFails=%d\n",
				o.Committed, o.Primary, o.FailedOp, len(o.CompFailures))

			if r.Committed != o.Committed || r.Primary != o.Primary ||
				r.FailedOpIndex != o.FailedOp ||
				len(r.CompFailures) != len(o.CompFailures) {
				t.Fatalf("seed=%d step=%d verdict mismatch:\nengine=%+v\nnaive =%+v\n\n%s",
					seed, step, r, o, log.String())
			}
			sortComp(r.CompFailures)
			sortComp(o.CompFailures)
			for i := range r.CompFailures {
				if r.CompFailures[i].OpIndex != o.CompFailures[i].OpIndex ||
					r.CompFailures[i].Class != o.CompFailures[i].Class {
					t.Fatalf("seed=%d step=%d comp failure mismatch:\n%+v\n%+v\n\n%s",
						seed, step, r.CompFailures[i], o.CompFailures[i], log.String())
				}
			}
			ea, ev, el, ep := engineWorld(g)
			sameWorld(t, fmt.Sprintf("seed=%d step=%d", seed, step),
				ea, o.Attrs, ev, o.Versions, el, o.Links, ep, o.Polluted)
		}
		t.Logf("seed %d: 60/60 differential steps matched; sample trace:\n%s",
			seed, head(log.String(), 12))
	}
}

func sortComp(cf []CompFailure) {
	sort.Slice(cf, func(i, j int) bool { return cf[i].OpIndex < cf[j].OpIndex })
}

func head(s string, lines int) string {
	all := strings.Split(s, "\n")
	if len(all) > lines {
		all = all[:lines]
	}
	return strings.Join(all, "\n")
}

// TestDifferentialConcurrentDisjoint: random actions on disjoint subsets run
// concurrently; their union must equal the naive serial execution of both.
func TestDifferentialConcurrentDisjoint(t *testing.T) {
	for seed := int64(100); seed < 130; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g := NewGraphWith(8)

		a1 := genAction(rng, 4, "left")
		a2 := genAction(rng, 4, "right")
		remapAction(&a1, 0)
		remapAction(&a2, 4)
		a1.Inject, a2.Inject = nil, nil

		var wg sync.WaitGroup
		wg.Add(2)
		var r1, r2 ActionResult
		go func() { defer wg.Done(); r1 = NewEngine(g).Execute(a1) }()
		go func() { defer wg.Done(); r2 = NewEngine(g).Execute(a2) }()
		wg.Wait()
		if !r1.Committed || !r2.Committed {
			t.Fatalf("seed=%d disjoint actions must both commit: %+v %+v",
				seed, r1, r2)
		}

		m := NewNaiveModel(NewGraphWith(8))
		m.Run(a1, nil)
		o := m.Run(a2, nil)
		ea, ev, el, ep := engineWorld(g)
		sameWorld(t, fmt.Sprintf("disjoint seed=%d", seed),
			ea, o.Attrs, ev, o.Versions, el, o.Links, ep, o.Polluted)
	}
}

// blockingChecker parks an action after its resources are acquired, giving a
// deterministic temporal overlap for contention tests.
type blockingChecker struct {
	hold     chan struct{}
	acquired chan struct{}
	once     sync.Once
}

func (b *blockingChecker) Check(_ *Graph, op SubOp) error {
	if op.Object == "o0" && op.HookName == "block" {
		b.once.Do(func() { close(b.acquired) })
		<-b.hold
	}
	return nil
}

// TestDifferentialSameSubsetContention forces a real overlap: action1 holds
// o0 while parked inside its cascaded validation hook; action2 contends the
// same instance and is rejected before any observable mutation.
func TestDifferentialSameSubsetContention(t *testing.T) {
	g := NewGraphWith(4)
	before, bv, _, _ := engineWorld(g)
	bc := &blockingChecker{hold: make(chan struct{}), acquired: make(chan struct{})}

	a1 := Action{Name: "holder", Ops: []SubOp{
		{Kind: OpValidate, Object: "o0", HookName: "block"},
		{Kind: OpSetAttrs, Object: "o1", Attrs: Attrs{"v": 777}},
	}}
	a2 := Action{Name: "loser", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "o0", Attrs: Attrs{"v": 999}},
	}}

	var wg sync.WaitGroup
	wg.Add(2)
	var r1, r2 ActionResult
	go func() { defer wg.Done(); r1 = NewEngine(g).WithChecker(bc).Execute(a1) }()
	<-bc.acquired
	loserDone := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(loserDone)
		r2 = NewEngine(g).Execute(a2)
	}()

	select {
	case <-loserDone:
	case <-time.After(2 * time.Second):
		t.Fatal("loser did not return while holder held the resource")
	}
	// Holder is still parked: any change here can only be the loser's.
	ea, ev, _, _ := engineWorld(g)
	if fmt.Sprint(ea) != fmt.Sprint(before) || fmt.Sprint(ev) != fmt.Sprint(bv) {
		t.Fatalf("loser mutated state before rejection:\nattrs=%v\nver=%v", ea, ev)
	}
	close(bc.hold)
	wg.Wait()

	if !r1.Committed {
		t.Fatalf("holder should commit after release: %+v", r1)
	}
	if r2.Committed || r2.Primary != ReasonContention {
		t.Fatalf("loser must be contention-rejected: %+v", r2)
	}
	m := NewNaiveModel(NewGraphWith(4))
	o := m.Run(a1, nil)
	ea, ev, el, ep := engineWorld(g)
	sameWorld(t, "forced contention", ea, o.Attrs, ev, o.Versions, el, o.Links, ep, o.Polluted)
}

func remapID(id InstanceID, base int) InstanceID {
	var n int
	fmt.Sscanf(string(id), "o%d", &n)
	return InstanceID(fmt.Sprintf("o%d", (n%4)+base))
}

func remapAction(a *Action, base int) {
	for i := range a.Ops {
		op := &a.Ops[i]
		op.Object = remapID(op.Object, base)
		if op.Kind == OpCreateLink || op.Kind == OpDeleteLink {
			op.Link.Source = remapID(op.Link.Source, base)
			op.Link.Target = remapID(op.Link.Target, base)
		}
	}
}

// NewGraphWith builds a clean graph with n objects o0..n-1 at version 0.
func NewGraphWith(n int) *Graph {
	g := NewGraph()
	for i := 0; i < n; i++ {
		g.AddObject(Object{ID: InstanceID(fmt.Sprintf("o%d", i)),
			Attrs: Attrs{"v": 0}, Version: 0})
	}
	return g
}
