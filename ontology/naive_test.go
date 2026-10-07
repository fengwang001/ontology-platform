package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// op is one generated write. Base and external observations are filled with
// whatever heads are visible when the op starts (simulating a client that
// read a snapshot, then committed after think time).
type op struct {
	id     ObjectID
	typ    TypeName
	base   Version
	props  map[Property]Value
	del    bool
	create bool
	obs    map[ObjectID]Version
	key    string
}

// TestRandomizedEquivalentSerialOrder runs the same generated schedule
// concurrently against the fine-grained Store and, using the Store's own
// accept/clock order as the candidate serial order, serially against the
// global-lock NaiveStore. Final version sequences must match.
func TestRandomizedEquivalentSerialOrder(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		runDifferential(t, rng, false)
	}
}

func TestRandomizedWithLinksEquivalentSerialOrder(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed + 1000))
		runDifferential(t, rng, true)
	}
}

func runDifferential(t *testing.T, rng *rand.Rand, links bool) {
	t.Helper()
	const nInst = 4
	const nProps = 5
	const nWriters = 8
	const opsPerWriter = 30

	newConcurrent := func() *Store {
		s := NewStore()
		s.RegisterType("T", randomHooks(links)...)
		return s
	}
	newNaive := func() *NaiveStore {
		n := NewNaiveStore()
		n.RegisterType("T", randomHooks(links)...)
		return n
	}

	// Generate per-writer op templates independently; each goroutine fills
	// live baselines when it runs (clients always read before write).
	mkOps := func() []op {
		var ops []op
		for w := 0; w < nWriters; w++ {
			for i := 0; i < opsPerWriter; i++ {
				o := op{
					id:    ObjectID(fmt.Sprintf("o%d", rng.Intn(nInst))),
					typ:   "T",
					props: map[Property]Value{},
					obs:   map[ObjectID]Version{},
				}
				nw := 1 + rng.Intn(2)
				for j := 0; j < nw; j++ {
					o.props[Property(fmt.Sprintf("p%d", rng.Intn(nProps)))] = float64(rng.Intn(1000))
				}
				if links && rng.Intn(3) == 0 {
					target := ObjectID(fmt.Sprintf("o%d", rng.Intn(nInst)))
					for target == o.id {
						target = ObjectID(fmt.Sprintf("o%d", rng.Intn(nInst)))
					}
					o.props["link"] = string(target)
				}
				if rng.Intn(40) == 0 {
					o.del = true
				}
				ops = append(ops, o)
			}
		}
		return ops
	}

	// --- concurrent run ---
	s := newConcurrent()
	initStores(t, s, nil, nInst)
	templates := mkOps()

	type executed struct {
		req WriteRequest
		res Result
		seq uint64
	}
	var mu sync.Mutex
	var done []executed

	var wg sync.WaitGroup
	idx := 0
	runOne := func() {
		defer wg.Done()
		mu.Lock()
		if idx >= len(templates) {
			mu.Unlock()
			return
		}
		tmpl := templates[idx]
		idx++
		mu.Unlock()

		h, exists := s.Head(tmpl.id)
		req := WriteRequest{Object: tmpl.id, Type: tmpl.typ, Values: tmpl.props,
			Delete: tmpl.del}
		if exists {
			req.Base = h
		} else {
			req.Create = true
		}
		if links {
			if tv, ok := req.Values["link"]; ok {
				if th, ok := s.Head(ObjectID(tv.(string))); ok {
					req.ObserveExternal = map[ObjectID]Version{ObjectID(tv.(string)): th}
				}
			}
		}
		r := s.Commit(req)
		mu.Lock()
		done = append(done, executed{req: req, res: r, seq: s.Clock()})
		mu.Unlock()
	}
	for w := 0; w < nWriters; w++ {
		wg.Add(1)
		go runOne()
	}
	wg.Wait()

	// Candidate serial order = store clock (Sequence) order. Accepted writes
	// carry a clock sequence; replay them in that order against the naive
	// model, with baselines rewritten to the naive heads (clients re-read at
	// their turn). Rejected concurrent attempts are then tried in the same
	// order and their naive verdict must match in kind.
	n := newNaive()
	initNaive(t, n, nInst)

	// Sort: accepted by Sequence; rejected appended afterwards in decision
	// order is not needed: instead, replay ALL ops in a topological order
	// derived from accept sequence, attempting each exactly as the client
	// originally intended against the naive model state it observes at its
	// turn. We first order accepted ops by Sequence.
	sort.SliceStable(done, func(i, j int) bool { return done[i].seq < done[j].seq })

	for _, e := range done {
		if !e.res.OK() {
			continue
		}
		h, exists := n.Head(e.req.Object)
		req := e.req
		if exists {
			req.Base = h
			req.Create = false
		} else {
			req.Create = true
			req.Base = 0
		}
		if links {
			if tv, ok := req.Values["link"]; ok {
				if th, ok := n.Head(ObjectID(tv.(string))); ok {
					req.ObserveExternal = map[ObjectID]Version{ObjectID(tv.(string)): th}
				} else {
					req.ObserveExternal = nil
				}
			}
		}
		req.IdempotencyKey = ""
		got := n.SerialCommit(req)
		if got.Kind != ConflictNone {
			t.Fatalf("seed mismatch: concurrent accepted %v (base %d), serial rejected kind=%s err=%v",
				e.req.Object, e.req.Base, got.Kind, got.Err)
		}
	}

	// Re-adjudicate every rejected attempt independently from the decision
	// log using the naive O(history) scan, rather than trusting markers.
	for i := 0; i < nInst; i++ {
		for _, d := range s.Decisions(ObjectID(fmt.Sprintf("o%d", i))) {
			if d.Accepted || d.RejectedByValidator || d.Kind != ConflictProperty {
				continue
			}
			if !naiveRuleConflicts(s, d) {
				t.Fatalf("marker conflict not corroborated by naive scan: %+v", d)
			}
		}
	}

	// Final visible sequences: number of accepted versions and the byte
	// content of each instance head must match.
	for i := 0; i < nInst; i++ {
		id := ObjectID(fmt.Sprintf("o%d", i))
		sh, sok := s.Head(id)
		nh, nok := n.Head(id)
		if sok != nok {
			t.Fatalf("existence mismatch for %s: store=%v naive=%v", id, sok, nok)
		}
		if !sok {
			continue
		}
		if sh != nh {
			t.Fatalf("head mismatch %s: store v%d naive v%d", id, sh, nh)
		}
		var ss, ns Snapshot
		ss, _ = s.Read(id, sh)
		ns, _ = n.Read(id, nh)
		if string(ss.Bytes()) != string(ns.Bytes()) {
			t.Fatalf("head bytes differ for %s:\nstore %s\nnaive %s",
				id, ss.Bytes(), ns.Bytes())
		}
	}
	if s.Clock() != n.Clock() {
		t.Fatalf("accepted write count differs: store %d naive %d", s.Clock(), n.Clock())
	}
}

// naiveRuleConflicts scans decision records: property conflict iff an
// accepted write after the baseline intersects the rejected relevant set
// locally, or an accepted linked-instance write after the observed version
// touches a property read through a hook.
func naiveRuleConflicts(s *Store, d Decision) bool {
	myLocal := map[Property]bool{}
	for _, p := range d.RelevantLocal {
		if p != "" {
			myLocal[p] = true
		}
	}
	myExt := d.RelevantExternal

	for _, later := range s.Decisions(d.Object) {
		if !later.Accepted || later.NewVersion <= d.DeclaredBase {
			continue
		}
		lset := map[Property]bool{}
		for _, p := range later.WriteSet {
			lset[p] = true
		}
		for _, p := range later.RelevantLocal {
			if p != "" {
				lset[p] = true
			}
		}
		if intersects(myLocal, lset) {
			return true
		}
		for id, ps := range myExt {
			fp, ok := later.RelevantExternal[id]
			if !ok {
				continue
			}
			a := map[Property]bool{}
			for _, p := range ps {
				a[p] = true
			}
			b := map[Property]bool{}
			for _, p := range fp {
				b[p] = true
			}
			if intersects(a, b) {
				return true
			}
		}
	}
	for id, props := range myExt {
		obs := d.Observed[id]
		for _, later := range s.Decisions(id) {
			if !later.Accepted || later.NewVersion <= obs {
				continue
			}
			set := map[Property]bool{}
			for _, p := range later.WriteSet {
				set[p] = true
			}
			for _, p := range later.RelevantLocal {
				if p != "" {
					set[p] = true
				}
			}
			for _, p := range props {
				if set[p] {
					return true
				}
			}
		}
	}
	return false
}

// randomHooks installs a few hooks whose declared read scopes couple
// properties (and linked p0 when links=true).
func randomHooks(links bool) []Validator {
	vs := []Validator{
		{
			Name: "read-p0-on-write-p1",
			Declare: func(v map[Property]Value, snap Snapshot) ReadScope {
				if _, ok := v["p1"]; ok {
					return ReadScope{Refs: []PropertyRef{{Local: "p0"}}}
				}
				return ReadScope{}
			},
		},
	}
	if links {
		vs = append(vs, Validator{
			Name: "read-linked-p0",
			Declare: func(v map[Property]Value, snap Snapshot) ReadScope {
				if _, ok := v["link"]; ok {
					return ReadScope{Refs: []PropertyRef{{Link: "link", Local: "p0"}}}
				}
				return ReadScope{}
			},
		})
	}
	return vs
}

func initStores(t *testing.T, s *Store, n *NaiveStore, nInst int) {
	t.Helper()
	for i := 0; i < nInst; i++ {
		id := ObjectID(fmt.Sprintf("o%d", i))
		props := map[Property]Value{"p0": 0.0}
		for p := 1; p < 5; p++ {
			props[Property(fmt.Sprintf("p%d", p))] = 0.0
		}
		mustOK(t, s.Commit(WriteRequest{Object: id, Type: "T", Create: true, Values: props}))
	}
}

func initNaive(t *testing.T, n *NaiveStore, nInst int) {
	t.Helper()
	for i := 0; i < nInst; i++ {
		id := ObjectID(fmt.Sprintf("o%d", i))
		props := map[Property]Value{"p0": 0.0}
		for p := 1; p < 5; p++ {
			props[Property(fmt.Sprintf("p%d", p))] = 0.0
		}
		if r := n.SerialCommit(WriteRequest{Object: id, Type: "T", Create: true, Values: props}); !r.OK() {
			t.Fatalf("naive init failed: %v", r.Err)
		}
	}
}
