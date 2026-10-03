package roadnet

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// --- Naive reference: a per-integer-time Dijkstra over the
// time-expanded network, written independently of the production
// evaluation path. ---

// naiveRecords reimplements version filtering for the reference.
func naiveRecords(e *edge, ver int64) []record {
	var recs []record
	for _, r := range e.records {
		if r.regVer <= ver && (r.replVer == 0 || r.replVer > ver) {
			recs = append(recs, r)
		}
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].eff < recs[j].eff })
	return recs
}

// naiveCostAt returns the cost of departing exactly at t, or -1.
func naiveCostAt(recs []record, t int64) int64 {
	var prof []Segment
	base := int64(0)
	for _, r := range recs {
		if r.eff <= t {
			prof, base = r.profile, r.eff
		}
	}
	if prof == nil {
		return -1
	}
	cost := int64(-1)
	for _, s := range prof {
		if base+s.Offset <= t {
			cost = s.Cost
		}
	}
	return cost
}

// naiveEval scans every integer departure time t' >= t up to the
// horizon (the last segment start; waiting beyond it never helps).
func naiveEval(recs []record, t int64) (int64, bool) {
	horizon := t
	for _, r := range recs {
		if r.eff+r.profile[len(r.profile)-1].Offset > horizon {
			horizon = r.eff + r.profile[len(r.profile)-1].Offset
		}
	}
	best := int64(math.MaxInt64)
	for tp := t; tp <= horizon && tp < best; tp++ {
		if c := naiveCostAt(recs, tp); c > 0 && tp+c < best {
			best = tp + c
		}
	}
	if best == math.MaxInt64 {
		return 0, false
	}
	return best, true
}

// naiveAll runs Dijkstra over the whole network with naiveEval and
// returns the earliest arrival at every node.
func naiveAll(nt *Net, s int, t0 int64, ver int64) []int64 {
	const inf = int64(math.MaxInt64)
	d := make([]int64, nt.n)
	for i := range d {
		d[i] = inf
	}
	d[s] = t0
	queue := &pq{{dist: t0, node: s}}
	heap.Init(queue)
	for queue.Len() > 0 {
		it := heap.Pop(queue).(pqItem)
		if it.dist > d[it.node] {
			continue
		}
		for _, eid := range nt.adj[it.node] {
			e := nt.edges[eid-1]
			if e.regVer > ver {
				continue
			}
			if arr, ok := naiveEval(naiveRecords(e, ver), d[it.node]); ok && arr < d[e.v] {
				d[e.v] = arr
				heap.Push(queue, pqItem{dist: arr, node: e.v})
			}
		}
	}
	return d
}

// --- Randomized cross-check. ---

func randProfile(r *rand.Rand) []Segment {
	n := 1 + r.Intn(4)
	p := make([]Segment, n)
	off := int64(0)
	for i := range p {
		if i > 0 {
			off += 1 + r.Int63n(8)
		}
		cost := int64(1 + r.Intn(9))
		if r.Intn(4) == 0 {
			cost = -1
		}
		p[i] = Segment{off, cost}
	}
	return p
}

func dumpNet(nt *Net) string {
	out := fmt.Sprintf("N=%d now=%d version=%d\n", nt.n, nt.now, nt.version)
	for _, e := range nt.edges {
		out += fmt.Sprintf("edge %d: %d->%d regVer=%d\n", e.id, e.u, e.v, e.regVer)
		for _, r := range e.records {
			out += fmt.Sprintf("  rec eff=%d profile=%v regVer=%d replVer=%d\n",
				r.eff, r.profile, r.regVer, r.replVer)
		}
	}
	return out
}

// checkLegs validates a returned route against the naive distances.
func checkLegs(t *testing.T, nt *Net, ver int64, s int, t0 int64, g int, res Result, d []int64) {
	t.Helper()
	if len(res.Legs) == 0 {
		if s != g {
			t.Fatalf("empty route for s!=g")
		}
		return
	}
	prevArr := t0
	prevNode := s
	for i, leg := range res.Legs {
		e := nt.edges[leg.EdgeID-1]
		if e.u != prevNode {
			t.Fatalf("leg %d: edge %d starts at %d, want %d", i, leg.EdgeID, e.u, prevNode)
		}
		if leg.Depart < prevArr {
			t.Fatalf("leg %d: depart %d before previous arrival %d", i, leg.Depart, prevArr)
		}
		if leg.Depart < d[e.u] {
			t.Fatalf("leg %d: depart %d before d(%d)=%d", i, leg.Depart, e.u, d[e.u])
		}
		c := naiveCostAt(naiveRecords(e, ver), leg.Depart)
		if c <= 0 || leg.Depart+c != leg.Arrive {
			t.Fatalf("leg %d: depart %d cost %d does not arrive at %d", i, leg.Depart, c, leg.Arrive)
		}
		// The departure must be the smallest integer >= d(u)
		// achieving the leg's arrival time.
		recs := naiveRecords(e, ver)
		for tp := d[e.u]; tp < leg.Depart; tp++ {
			if ct := naiveCostAt(recs, tp); ct > 0 && tp+ct == leg.Arrive {
				t.Fatalf("leg %d: depart %d not minimal, %d also arrives at %d",
					i, leg.Depart, tp, leg.Arrive)
			}
		}
		if leg.Arrive != d[e.v] {
			t.Fatalf("leg %d: arrive %d != d(%d)=%d (edge not tight)", i, leg.Arrive, e.v, d[e.v])
		}
		prevArr, prevNode = leg.Arrive, e.v
	}
	if prevNode != g {
		t.Fatalf("route ends at %d, want %d", prevNode, g)
	}
	if prevArr != res.Arrival {
		t.Fatalf("route arrival %d != result arrival %d", prevArr, res.Arrival)
	}
}

// TestRandomCrossCheck drives random networks and queries and
// compares against the naive per-time Dijkstra.
func TestRandomCrossCheck(t *testing.T) {
	const cases = 2000
	r := rand.New(rand.NewSource(20261003))
	mismatches := 0
	for tc := 0; tc < cases; tc++ {
		n := 2 + r.Intn(7)
		nt, err := NewNet(n, 40)
		if err != nil {
			t.Fatal(err)
		}
		var ops []string
		nEdges := 1 + r.Intn(12)
		for i := 0; i < nEdges; i++ {
			u, v := r.Intn(n), r.Intn(n)
			for v == u {
				v = r.Intn(n)
			}
			p := randProfile(r)
			if _, err := nt.AddEdge(u, v, p); err != nil {
				t.Fatalf("case %d: AddEdge: %v", tc, err)
			}
			ops = append(ops, fmt.Sprintf("AddEdge(%d,%d,%v)", u, v, p))
		}
		nAnn := r.Intn(6)
		for i := 0; i < nAnn; i++ {
			if r.Intn(3) == 0 {
				adv := nt.now + r.Int63n(6)
				if err := nt.Advance(adv); err != nil {
					t.Fatalf("case %d: Advance(%d): %v", tc, adv, err)
				}
				ops = append(ops, fmt.Sprintf("Advance(%d)", adv))
			}
			e := nt.edges[r.Intn(len(nt.edges))]
			last := e.lastLive()
			eff := last.eff + r.Int63n(8)
			if eff < nt.now {
				eff = nt.now
			}
			if eff > MaxTime {
				continue
			}
			p := randProfile(r)
			if err := nt.Announce(e.id, eff, p); err != nil {
				t.Fatalf("case %d: Announce(%d,%d): %v", tc, e.id, eff, err)
			}
			ops = append(ops, fmt.Sprintf("Announce(%d,%d,%v)", e.id, eff, p))
		}
		curVer := nt.Version()
		for q := 0; q < 4; q++ {
			s, g := r.Intn(n), r.Intn(n)
			t0 := r.Int63n(31)
			var ver *int64
			v := curVer
			if r.Intn(2) == 0 {
				v = r.Int63n(curVer + 1)
				ver = &v
			}
			res, err := nt.EarliestArrival(s, t0, g, ver)
			d := naiveAll(nt, s, t0, v)
			label := fmt.Sprintf("case %d query EA(%d,%d,%d,ver=%d)", tc, s, t0, g, v)
			if d[g] == math.MaxInt64 {
				if err == nil {
					t.Fatalf("%s: got arrival %d, naive says unreachable\n%s",
						label, res.Arrival, dumpNet(nt))
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s: unexpected err %v (naive arrival %d)\n%s", label, err, d[g], dumpNet(nt))
			}
			if res.Arrival != d[g] {
				mismatches++
				t.Fatalf("%s: arrival %d != naive %d\nops=%v\n%s",
					label, res.Arrival, d[g], ops, dumpNet(nt))
			}
			checkLegs(t, nt, v, s, t0, g, res, d)
			// popped must not exceed the number of nodes with d <= d(g).
			settled := 0
			for _, dx := range d {
				if dx <= d[g] {
					settled++
				}
			}
			if res.Popped > settled {
				t.Fatalf("%s: popped %d > settled %d", label, res.Popped, settled)
			}
			if tc < 3 {
				t.Logf("%s -> arrival=%d legs=%v popped=%d settled<=%d (判定依据: 朴素逐时刻Dijkstra)",
					label, res.Arrival, res.Legs, res.Popped, settled)
			}
		}
	}
	t.Logf("随机对照完成: %d 组网路与查询全部与朴素逐时刻最短路一致", cases)
	_ = mismatches
}

// TestReplayDeterminism: the same operation sequence replayed on a
// fresh network yields byte-identical results, and historical queries
// are immune to later operations.
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(777))
	type op struct {
		kind    int // 0 add, 1 announce, 2 advance
		u, v    int
		eff     int64
		profile []Segment
	}
	var ops []op
	n := 6
	now := int64(0)
	effs := map[int]int64{}
	edgeID := 0
	for i := 0; i < 60; i++ {
		switch r.Intn(3) {
		case 0:
			u, v := r.Intn(n), r.Intn(n)
			for v == u {
				v = r.Intn(n)
			}
			ops = append(ops, op{kind: 0, u: u, v: v, profile: randProfile(r)})
			edgeID++
			effs[edgeID] = 0
		case 1:
			if edgeID == 0 {
				continue
			}
			id := 1 + r.Intn(edgeID)
			eff := effs[id] + r.Int63n(5)
			if eff < now {
				eff = now
			}
			ops = append(ops, op{kind: 1, u: id, eff: eff, profile: randProfile(r)})
			effs[id] = eff
		case 2:
			now += r.Int63n(4)
			ops = append(ops, op{kind: 2, eff: now})
		}
	}
	apply := func(nt *Net) {
		for _, o := range ops {
			switch o.kind {
			case 0:
				if _, err := nt.AddEdge(o.u, o.v, o.profile); err != nil {
					t.Fatalf("replay AddEdge: %v", err)
				}
			case 1:
				if err := nt.Announce(o.u, o.eff, o.profile); err != nil {
					t.Fatalf("replay Announce: %v", err)
				}
			case 2:
				if err := nt.Advance(o.eff); err != nil {
					t.Fatalf("replay Advance: %v", err)
				}
			}
		}
	}
	nt1 := newNet(t, n, 200)
	apply(nt1)
	nt2 := newNet(t, n, 200)
	apply(nt2)

	// Historical snapshots: query every version before and after
	// extra mutations; results must be identical everywhere.
	curVer := nt1.Version()
	snap := map[int64]Result{}
	for v := int64(0); v <= curVer; v++ {
		res, err := nt1.EarliestArrival(0, 3, n-1, verPtr(v))
		if err != nil {
			continue
		}
		snap[v] = res
	}
	// Extra mutations on nt1 must not disturb already-produced versions.
	for i := 0; i < 10; i++ {
		id := 1 + r.Intn(edgeID)
		eff := effs[id] + 1 + r.Int63n(5)
		if eff < nt1.Now() {
			eff = nt1.Now()
		}
		if err := nt1.Announce(id, eff, randProfile(r)); err != nil {
			t.Fatalf("extra Announce: %v", err)
		}
		effs[id] = eff
	}
	if err := nt1.Advance(nt1.Now() + 10); err != nil {
		t.Fatal(err)
	}
	for v, want := range snap {
		got1, err := nt1.EarliestArrival(0, 3, n-1, verPtr(v))
		if err != nil {
			t.Fatalf("ver %d: query failed after later ops: %v", v, err)
		}
		got2, err := nt2.EarliestArrival(0, 3, n-1, verPtr(v))
		if err != nil {
			t.Fatalf("ver %d: replayed query failed: %v", v, err)
		}
		if !reflect.DeepEqual(got1, want) || !reflect.DeepEqual(got2, want) {
			t.Fatalf("ver %d: results diverged: before=%v after=%v replay=%v", v, want, got1, got2)
		}
	}
	t.Logf("重放与历史稳定性: %d 个版本快照在后续操作与整体重放下逐字一致", len(snap))
}
