package ontology

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

func fmtSubjectID(i int) SubjectID { return SubjectID("u" + strconv.Itoa(i)) }

// queryTrace is one recorded differential-test query with its input, output
// and the per-step overlay basis.
type queryTrace struct {
	Seed         int64       `json:"seed"`
	Subject      SubjectID   `json:"subject"`
	From         ObjectID    `json:"from"`
	To           ObjectID    `json:"to"`
	StateVersion int64       `json:"state_version"`
	Status       string      `json:"status"`
	Path         []ObjectID  `json:"path,omitempty"`
	Cost         float64     `json:"cost,omitempty"`
	Counters     Counters    `json:"counters"`
	Steps        []stepTrace `json:"steps"`
}

type stepTrace struct {
	From     ObjectID   `json:"from"`
	To       ObjectID   `json:"to"`
	LinkType LinkType   `json:"link_type"`
	Cost     float64    `json:"cost"`
	Verdict  string     `json:"verdict"`
	Reason   string     `json:"reason"`
	FromType ObjectType `json:"from_type"`
	ToType   ObjectType `json:"to_type"`
}

type rngWorld struct {
	ps       *PermissionState
	g        *Graph
	eng      *QueryEngine
	subjects []SubjectID
	objects  []ObjectID
	rng      *rand.Rand
}

func buildRandomWorld(seed int64) *rngWorld {
	rng := rand.New(rand.NewSource(seed))
	w := &rngWorld{
		ps:  NewPermissionState(),
		g:   NewGraph(),
		rng: rng,
	}
	w.eng = NewQueryEngine(w.ps, w.g)

	nGroups := 1 + rng.Intn(5)
	nSubjects := 1 + rng.Intn(4)
	nObjects := 2 + rng.Intn(7)
	objectTypes := []ObjectType{"A", "B", "C", "D"}
	linkTypes := []LinkType{"e1", "e2", "e3"}

	for i := 0; i < nGroups; i++ {
		gid := GroupID("g" + string(rune('a'+i)))
		_ = w.ps.UpsertGroup(gid, rng.Intn(4))
	}
	groupIDs := make([]GroupID, 0, nGroups)
	for i := 0; i < nGroups; i++ {
		groupIDs = append(groupIDs, GroupID("g"+string(rune('a'+i))))
	}
	for i := 0; i < nSubjects; i++ {
		sid := SubjectID(fmtSubjectID(i))
		w.subjects = append(w.subjects, sid)
		for _, gid := range groupIDs {
			if rng.Intn(2) == 0 {
				_ = w.ps.AddMember(sid, gid)
			}
		}
	}
	for i := 0; i < nObjects; i++ {
		oid := ObjectID("n" + string(rune('a'+i%26)))
		w.objects = append(w.objects, oid)
		ty := objectTypes[rng.Intn(len(objectTypes))]
		_ = w.g.AddObject(oid, ty)
	}
	// Random sparse directed links.
	for i := 0; i < nObjects*2; i++ {
		from := w.objects[rng.Intn(len(w.objects))]
		to := w.objects[rng.Intn(len(w.objects))]
		lt := linkTypes[rng.Intn(len(linkTypes))]
		_ = w.g.AddLink(Link{Type: lt, From: from, To: to, Cost: float64(rng.Intn(3)) + 0.5})
	}
	// Random declarations.
	decisions := []Decision{Allow, Deny, Unset}
	for _, gid := range groupIDs {
		for _, ty := range objectTypes {
			if rng.Intn(2) == 0 {
				_ = w.ps.SetObjectDecl(gid, ty, decisions[rng.Intn(len(decisions))])
			}
		}
		for _, lt := range linkTypes {
			if rng.Intn(2) == 0 {
				_ = w.ps.SetLinkDecl(gid, lt, decisions[rng.Intn(2)])
			}
		}
	}
	return w
}

// bruteForcePath independently enumerates simple paths using the naive
// resolver per edge. It returns the same semantics as the engine:
// guaranteed path (only allowed edges), optimistic path (ambiguous allowed),
// and the resulting status.
func bruteForcePath(w *rngWorld, snap *snapshot, gs *graphSnapshot, q Query) (
	guaranteed, optimistic *nodeResult,
) {
	type route struct {
		nodes []ObjectID
		links []LinkType
		cost  float64
	}
	var bestAllow, bestAmbig *route
	better := func(a, b *route) bool {
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		if a.cost != b.cost {
			return a.cost < b.cost
		}
		return lexLess(a.nodes, b.nodes)
	}
	var dfs func(cur ObjectID, r route, visited map[ObjectID]bool)
	dfs = func(cur ObjectID, r route, visited map[ObjectID]bool) {
		if cur == q.To {
			// Reached the target only on allowed edges (ambiguous edges are
			// never taken here).
			if better(&r, bestAllow) {
				cp := r
				bestAllow = &cp
			}
			return
		}
		for _, ed := range gs.out[cur] {
			if visited[ed.to] {
				continue
			}
			ft := gs.objects[cur].Type
			v := naiveResolve(snap, q.Subject, ed.link.Type, ft, ed.t)
			if v.Verdict != VerdictAllow {
				continue
			}
			visited[ed.to] = true
			dfs(ed.to, route{
				nodes: appendCopy(r.nodes, ed.to),
				links: appendCopy(r.links, ed.link.Type),
				cost:  r.cost + ed.link.Cost,
			}, visited)
			delete(visited, ed.to)
		}
	}
	dfs(q.From, route{nodes: []ObjectID{q.From}}, map[ObjectID]bool{q.From: true})

	var dfsOpt func(cur ObjectID, r route, visited map[ObjectID]bool)
	dfsOpt = func(cur ObjectID, r route, visited map[ObjectID]bool) {
		if cur == q.To {
			if better(&r, bestAmbig) {
				cp := r
				bestAmbig = &cp
			}
			return
		}
		for _, ed := range gs.out[cur] {
			if visited[ed.to] {
				continue
			}
			ft := gs.objects[cur].Type
			v := naiveResolve(snap, q.Subject, ed.link.Type, ft, ed.t)
			if v.Verdict != VerdictAllow && v.Verdict != VerdictAmbiguous {
				continue
			}
			visited[ed.to] = true
			dfsOpt(ed.to, route{
				nodes: appendCopy(r.nodes, ed.to),
				links: appendCopy(r.links, ed.link.Type),
				cost:  r.cost + ed.link.Cost,
			}, visited)
			delete(visited, ed.to)
		}
	}
	dfsOpt(q.From, route{nodes: []ObjectID{q.From}}, map[ObjectID]bool{q.From: true})

	toNR := func(r *route) *nodeResult {
		if r == nil {
			return nil
		}
		return &nodeResult{nodes: r.nodes, cost: r.cost}
	}
	return toNR(bestAllow), toNR(bestAmbig)
}

type nodeResult struct {
	nodes []ObjectID
	cost  float64
}

func expectedStatus(guaranteed, optimistic *nodeResult) Status {
	switch {
	case guaranteed != nil && optimistic != nil && sameSeq(guaranteed.nodes, optimistic.nodes):
		return StatusReachable
	case guaranteed != nil:
		return StatusAmbiguous
	case optimistic != nil:
		return StatusAmbiguous
	default:
		return StatusUnreachable
	}
}

func TestDifferentialAgainstNaiveModel(t *testing.T) {
	if testing.Short() {
		t.Skip("differential test skipped in -short mode")
	}
	var traces []queryTrace
	const iterations = 400

	for seed := int64(1); seed <= iterations; seed++ {
		w := buildRandomWorld(seed)
		snap := w.ps.Snapshot()
		gs := w.g.snapshot()

		// Per-edge cross-check: the real resolver and the naive model must
		// agree on every signature reachable in the graph.
		for _, s := range w.subjects {
			for from, edges := range gs.out {
				ft := gs.objects[from].Type
				for _, ed := range edges {
					want := naiveResolve(snap, s, ed.link.Type, ft, ed.t)
					got := DecideAt(snap, s, ed.link.Type, ft, ed.t)
					if got.Verdict != want.Verdict || got.Reason != want.Reason {
						t.Fatalf("seed=%d resolver mismatch: got %s/%s naive %s/%s",
							seed, got.Verdict, got.Reason, want.Verdict, want.Reason)
					}
				}
			}
		}

		// A handful of random point-to-point queries per world.
		for i := 0; i < 5; i++ {
			s := w.subjects[w.rng.Intn(len(w.subjects))]
			from := w.objects[w.rng.Intn(len(w.objects))]
			to := w.objects[w.rng.Intn(len(w.objects))]
			q := Query{Subject: s, From: from, To: to}

			res := w.eng.shortestPathAt(q, snap, gs)
			gp, op := bruteForcePath(w, snap, gs, q)
			wantStatus := expectedStatus(gp, op)
			if res.Status != wantStatus {
				t.Fatalf("seed=%d q=%v: engine=%s brute=%s", seed, q, res.Status, wantStatus)
			}
			if res.Status == StatusReachable {
				if gp == nil || res.Path.Cost != gp.cost ||
					!sameSeq(res.Path.ObjectIDs, gp.nodes) {
					t.Fatalf("seed=%d path mismatch: engine=%+v brute=%+v",
						seed, res.Path, gp)
				}
			}

			tr := queryTrace{
				Seed:         seed,
				Subject:      s,
				From:         from,
				To:           to,
				StateVersion: res.StateVersion,
				Status:       res.Status.String(),
				Counters:     res.Counters,
			}
			if res.Path != nil {
				tr.Path = res.Path.ObjectIDs
				tr.Cost = res.Path.Cost
			}
			for _, st := range res.Steps {
				tr.Steps = append(tr.Steps, stepTrace{
					From: st.From, To: st.To, LinkType: st.LinkType,
					Cost: st.LinkCost, Verdict: st.Verdict.Verdict.String(),
					Reason:   st.Verdict.Reason,
					FromType: st.Verdict.FromType, ToType: st.Verdict.ToType,
				})
			}
			traces = append(traces, tr)
		}
	}

	// Persist the recorded inputs/outputs/bases for local verification.
	dir := filepath.Join("..", "testdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "differential-traces.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	sort.Slice(traces, func(i, j int) bool { return traces[i].Seed < traces[j].Seed })
	for _, tr := range traces {
		if err := enc.Encode(tr); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %d differential queries to %s", len(traces), path)
}
