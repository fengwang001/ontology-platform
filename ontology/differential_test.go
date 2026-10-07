package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// genWorld builds a random schema and an initial committed population so that
// later batches can mix updates, creates and deletes.
func genWorld(rng *rand.Rand) (*Platform, *naiveModel, *naiveModel, []InstanceID, []InstanceID) {
	p := NewPlatform()
	p.SetJournal(NewJournal(""))
	p.RegisterObjectType(ObjectType{Name: "Person", Hook: func(cur, prop Properties) error {
		if prop != nil && prop["name"].(string) == "BAD" {
			return errStr("BAD rejected")
		}
		return nil
	}})
	p.RegisterObjectType(ObjectType{Name: "Group"})
	p.RegisterLinkType(LinkType{
		Name: "membership", LeftType: "Person", RightType: "Group",
		CardA: Cardinality{Min: 0, Max: 2}, CardB: Cardinality{Min: 0, Max: 2},
	})
	m := newNaive(p)

	np := 3 + rng.Intn(3)
	ng := 2 + rng.Intn(2)
	persons := make([]InstanceID, 0, np)
	groups := make([]InstanceID, 0, ng)
	var ops []Operation
	for i := 0; i < np; i++ {
		id := InstanceID(fmt.Sprintf("p%d", i))
		persons = append(persons, id)
		ops = append(ops, Operation{Instance: id, Type: "Person", BaseVersion: 0,
			Props: props(map[string]any{"name": fmt.Sprintf("p%d", i)})})
	}
	for i := 0; i < ng; i++ {
		id := InstanceID(fmt.Sprintf("g%d", i))
		groups = append(groups, id)
		ops = append(ops, Operation{Instance: id, Type: "Group", BaseVersion: 0,
			Props: props(map[string]any{"title": fmt.Sprintf("G%d", i)})})
	}
	b := Batch{ID: "genesis", Ops: ops}
	pr := p.Commit(b)
	nr := m.apply(b)
	if !pr.OK || !nr.ok {
		panic("genesis must commit")
	}
	clean := newNaive(p) // post-genesis reference, untouched by generation
	return p, m, clean, persons, groups
}

// genBatch builds one random batch. genModel is the serial generation-time
// state: base versions come from it, then the batch is applied to it, so that
// the sequence of declared bases mirrors a real client observing outcomes.
// Corruption (stale bases, BAD props) is injected randomly to create races.
func genBatch(rng *rand.Rand, seq int, persons, groups []InstanceID, genModel *naiveModel) Batch {
	b := Batch{ID: fmt.Sprintf("batch-%d", seq)}
	used := map[InstanceID]bool{}
	nOps := 1 + rng.Intn(3)
	for i := 0; i < nOps; i++ {
		id := persons[rng.Intn(len(persons))]
		if used[id] {
			continue
		}
		used[id] = true
		var base int64
		if in, ok := genModel.inst[id]; ok {
			base = in.version
		}
		if rng.Intn(4) == 0 {
			base += int64(rng.Intn(2) - 1) // stale/ahead -> possible conflict
			if base < 0 {
				base = 5
			}
		}
		var pr Properties
		if rng.Intn(6) == 0 {
			pr = nil // delete
		} else {
			name := fmt.Sprintf("%s-v%d", id, rng.Intn(4))
			if rng.Intn(12) == 0 {
				name = "BAD" // hook rejection
			}
			pr = props(map[string]any{"name": name})
		}
		b.Ops = append(b.Ops, Operation{Instance: id, Type: "Person", BaseVersion: base, Props: pr})
	}
	nLinks := rng.Intn(3)
	for i := 0; i < nLinks; i++ {
		a := persons[rng.Intn(len(persons))]
		g := groups[rng.Intn(len(groups))]
		_, exists := genModel.linkSet[normalizeLink("membership", a, g)]
		b.Links = append(b.Links, LinkOp{Link: "membership", A: a, B: g, Add: !exists})
	}
	genModel.apply(b) // generation-time simulation; discarded if it fails
	return b
}

type runRes struct {
	b       Batch
	ok      bool
	failure FailureClass
	tick    int64
}

func stateFingerprint(inst map[InstanceID]nInst) string {
	out := ""
	for id, in := range inst {
		out += fmt.Sprintf("%s:%s:%d:%v|", id, in.typ, in.version, in.props)
	}
	return out
}

func platformFingerprint(p *Platform) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := ""
	for id, in := range p.instances {
		out += fmt.Sprintf("%s:%s:%d:%v|", id, in.typ, in.version, in.props)
	}
	links := ""
	for k := range p.links {
		links += fmt.Sprintf("%s(%s,%s)|", k.link, k.a, k.b)
	}
	return out, links
}

func naiveLinksFingerprint(m *naiveModel) string {
	out := ""
	for k := range m.linkSet {
		out += fmt.Sprintf("%s(%s,%s)|", k.link, k.a, k.b)
	}
	return out
}

// findSerialOrder searches for a serialization of all batches whose naive
// serial execution reproduces the observed commit set and final state.
// Committed batches must appear in their observed commit (tick) order; failed
// batches may be interleaved anywhere and may fail for any reason — neither
// the failure class nor the naive replay tick value is intrinsic, since both
// depend on the serial position. Only the ok/outcome set and final state are
// linearizability requirements.
func findSerialOrder(t *testing.T, m0 *naiveModel, results []runRes) bool {
	t.Helper()
	var committed []runRes
	var failed []runRes
	for _, r := range results {
		if r.ok {
			committed = append(committed, r)
		} else {
			failed = append(failed, r)
		}
	}
	sortResults := func(rs []runRes) {
		// committed already sorted by tick at collection; sort defensively.
		sort.Slice(rs, func(i, j int) bool { return rs[i].tick < rs[j].tick })
	}
	sortResults(committed)

	okByID := make(map[string]bool, len(results))
	for _, r := range results {
		okByID[r.b.ID] = r.ok
	}
	var dfs func(ci int, remaining []runRes, m *naiveModel) bool
	dfs = func(ci int, remaining []runRes, m *naiveModel) bool {
		if len(remaining) == 0 {
			return ci == len(committed)
		}
		for i, r := range remaining {
			mm := cloneModel(m)
			nr := mm.apply(r.b)
			if nr.ok != okByID[r.b.ID] {
				continue // prune: outcome diverges from the observed one
			}
			if nr.ok {
				// Committed batches must be consumed in observed commit order.
				if r.b.ID != committed[ci].b.ID {
					continue
				}
				rest := append(append([]runRes{}, remaining[:i]...), remaining[i+1:]...)
				if dfs(ci+1, rest, mm) {
					return true
				}
			} else {
				rest := append(append([]runRes{}, remaining[:i]...), remaining[i+1:]...)
				if dfs(ci, rest, mm) {
					return true
				}
			}
		}
		return false
	}
	all := append(append([]runRes{}, committed...), failed...)
	return dfs(0, all, m0)
}

func cloneModel(m *naiveModel) *naiveModel {
	cp := &naiveModel{
		objects: m.objects, links: m.links,
		inst:    map[InstanceID]nInst{},
		linkSet: map[linkKey]struct{}{},
		tick:    m.tick,
	}
	for k, v := range m.inst {
		v.props = cloneProps(v.props)
		cp.inst[k] = v
	}
	for k := range m.linkSet {
		cp.linkSet[k] = struct{}{}
	}
	return cp
}

func TestRandomizedDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	for _, seed := range []int64{1, 2, 3, 7, 42, 99, 123, 256, 777, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			p, genm, clean, persons, groups := genWorld(rng)
			const nBatches = 8
			batches := make([]Batch, nBatches)
			for i := range batches {
				batches[i] = genBatch(rng, i, persons, groups, genm)
			}

			// Concurrent execution on the real platform.
			res := make([]runRes, nBatches)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range batches {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					r := p.Commit(batches[i])
					res[i] = runRes{b: batches[i], ok: r.OK, failure: r.Failure, tick: r.CommitTick}
				}(i)
			}
			close(start)
			wg.Wait()

			if !findSerialOrder(t, clean, res) {
				t.Fatalf("seed %d: concurrent outcome matches NO serial order: %+v", seed, res)
			}

			// Cross-check final state against one explicit serialization:
			// replay journal entries in seq order through a fresh naive model.
			jm := emptyNaive(p)
			for _, e := range p.journal.Entries() {
				b := recordToBatch(e.Batch)
				nr := jm.apply(b)
				if nr.ok != e.OK || (e.OK && nr.tick != e.Tick) {
					t.Fatalf("journal replay mismatch on %s: naive=%+v entry ok=%v tick=%d",
						e.BatchID, nr, e.OK, e.Tick)
				}
			}
			pf, pl := platformFingerprint(p)
			jf := stateFingerprint(jm.inst)
			jl := naiveLinksFingerprint(jm)
			if normalizeFP(pf) != normalizeFP(jf) {
				t.Fatalf("final instance state mismatch:\nplat=%s\nnaive=%s", pf, jf)
			}
			if normalizeFP(pl) != normalizeFP(jl) {
				t.Fatalf("final link state mismatch:\nplat=%s\nnaive=%s", pl, jl)
			}
		})
	}
}

func normalizeFP(s string) string {
	// fingerprints are pipe-joined unordered records; sort them.
	var recs []string
	cur := ""
	for _, ch := range s {
		if ch == '|' {
			recs = append(recs, cur)
			cur = ""
		} else {
			cur += string(ch)
		}
	}
	for i := 0; i < len(recs); i++ {
		for j := i + 1; j < len(recs); j++ {
			if recs[j] < recs[i] {
				recs[i], recs[j] = recs[j], recs[i]
			}
		}
	}
	out := ""
	for _, r := range recs {
		out += r + "|"
	}
	return out
}

func recordToBatch(rec BatchRecord) Batch {
	b := Batch{}
	for _, op := range rec.Ops {
		var pr Properties
		if !op.Delete {
			pr = Properties{}
			for k, v := range op.Props {
				pr[k] = v
			}
		}
		b.Ops = append(b.Ops, Operation{
			Instance: InstanceID(op.Instance), Type: TypeName(op.Type),
			BaseVersion: op.BaseVersion, Props: pr,
		})
	}
	for _, l := range rec.Links {
		b.Links = append(b.Links, LinkOp{Link: TypeName(l.Link), A: InstanceID(l.A), B: InstanceID(l.B), Add: l.Add})
	}
	return b
}
