package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// randomWorld builds the shared schema and a genesis scenario applied to
// every fresh engine, plus a set of random candidate batches whose baselines
// are declared against the genesis image only.
type randomWorld struct {
	reg       *Registry
	genesis   []BatchInput
	batches   []BatchInput
	genDigest string
}

func linearizabilityRegistry() *Registry {
	r := NewRegistry()
	r.RegisterObjectType(ObjectType{
		Name: "Person",
		Validate: ValidationHook{Run: func(p *Instance, v BatchView) string {
			if banned, _ := p.Properties["ban"].(string); banned != "" {
				return "banned property present"
			}
			return ""
		}},
	})
	r.RegisterObjectType(ObjectType{Name: "Team"})
	r.RegisterLinkType(LinkType{Name: "member", Source: "Person", Target: "Team", SrcMax: 2, DstMax: 0})
	r.RegisterLinkType(LinkType{Name: "lead", Source: "Person", Target: "Team", SrcMax: 1, DstMax: 1})
	return r
}

func genWorld(rng *rand.Rand) *randomWorld {
	w := &randomWorld{reg: linearizabilityRegistry()}

	people := []InstanceID{"p0", "p1", "p2", "p3"}
	teams := []InstanceID{"t0", "t1", "t2", "t3"}

	// Genesis: create every instance, then seed a deterministic mix of
	// member/lead edges so degree boundaries are close.
	create := BatchInput{ClientID: "genesis-create"}
	for _, id := range people {
		create.Items = append(create.Items, BatchItem{
			ID: id, Type: "Person", Create: true, Properties: Property{"n": string(id)},
		})
	}
	for _, id := range teams {
		create.Items = append(create.Items, BatchItem{
			ID: id, Type: "Team", Create: true, Properties: Property{"n": string(id)},
		})
	}
	seed := BatchInput{ClientID: "genesis-links"}
	for _, id := range people {
		seed.Items = append(seed.Items, BatchItem{
			ID: id, Type: "Person", Baseline: 1, Properties: Property{"n": string(id)},
		})
	}
	// p0 member of t0,t1 (degree exactly 2 at the boundary); p1 leads t0.
	seed.Items[0].LinkDeltas = []EdgeDelta{
		{Edge: Edge{"member", "p0", "t0"}, Add: true},
		{Edge: Edge{"member", "p0", "t1"}, Add: true},
		{Edge: Edge{"lead", "p0", "t2"}, Add: true},
	}
	seed.Items[1].LinkDeltas = []EdgeDelta{
		{Edge: Edge{"lead", "p1", "t0"}, Add: true},
	}
	w.genesis = []BatchInput{create, seed}

	// Genesis versions are 2 for people (touched by both batches), 1 teams.
	personVersion := func(id InstanceID) Version { return 2 }
	teamVersion := func(id InstanceID) Version { return 1 }

	allIDs := append(append([]InstanceID{}, people...), teams...)
	const n = 5
	for b := 0; b < n; b++ {
		label := fmt.Sprintf("B%d", b)
		batch := BatchInput{ClientID: label}
		k := 1 + rng.Intn(3) // 1..3 items
		used := map[InstanceID]bool{}
		for i := 0; i < k; i++ {
			id := allIDs[rng.Intn(len(allIDs))]
			if used[id] && rng.Intn(4) != 0 {
				id = allIDs[rng.Intn(len(allIDs))]
			}
			used[id] = true
			typ := ObjectTypeName("Team")
			base := teamVersion(id)
			if id[0] == 'p' {
				typ = "Person"
				base = personVersion(id)
			}
			props := Property{"n": string(id), "w": label}
			if rng.Intn(5) == 0 {
				props["ban"] = "yes" // random hook rejection
			}
			if rng.Intn(5) == 0 {
				base = 99 // random stale baseline
			}
			item := BatchItem{ID: id, Type: typ, Baseline: base, Properties: props}
			if id[0] == 'p' && rng.Intn(2) == 0 {
				team := teams[rng.Intn(len(teams))]
				link := LinkTypeName("member")
				if rng.Intn(3) == 0 {
					link = "lead"
				}
				add := rng.Intn(2) == 0
				item.LinkDeltas = append(item.LinkDeltas, EdgeDelta{
					Edge: Edge{link, id, team}, Add: add,
				})
			}
			batch.Items = append(batch.Items, item)
		}
		// Occasionally force an internal duplicate declaration.
		if rng.Intn(6) == 0 && len(batch.Items) > 0 {
			dup := batch.Items[0]
			dup.LinkDeltas = nil
			batch.Items = append(batch.Items, dup)
		}
		w.batches = append(w.batches, batch)
	}
	return w
}

func bootNaive(w *randomWorld) *naiveEngine {
	e := newNaiveEngine(w.reg)
	for _, in := range w.genesis {
		if v := e.apply(in); !v.committed {
			panic("naive genesis failed")
		}
	}
	return e
}

func bootStore(w *randomWorld) *Store {
	s := NewStore(w.reg)
	for _, in := range w.genesis {
		if _, err := s.ApplyBatch(in); err != nil {
			panic("store genesis failed: " + err.Error())
		}
	}
	return s
}

// outcomeKey is the serial-order-independent signature of one run: the
// verdict of every labeled batch plus the final state digest.
func outcomeKey(verdicts map[string]FailureKind, digest string) string {
	labels := make([]string, 0, len(verdicts))
	for l := range verdicts {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	var b strings.Builder
	for _, l := range labels {
		fmt.Fprintf(&b, "%s=%s;", l, verdicts[l])
	}
	b.WriteString("::")
	b.WriteString(digest)
	return b.String()
}

func TestRandomizedEquivalentToSomeSerialOrder(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for trial := 0; trial < 60; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)*7919 + 13))
		w := genWorld(rng)

		// Enumerate every serial permutation on the independent naive model.
		expected := map[string]bool{}
		n := len(w.batches)
		perm := make([]int, n)
		for i := range perm {
			perm[i] = i
		}
		var permute func(int)
		permute = func(pos int) {
			if pos == n {
				e := bootNaive(w)
				verdicts := map[string]FailureKind{}
				for _, idx := range perm {
					v := e.apply(w.batches[idx])
					verdicts[w.batches[idx].ClientID] = v.kind
				}
				expected[outcomeKey(verdicts, e.digest())] = true
				return
			}
			for j := pos; j < n; j++ {
				perm[pos], perm[j] = perm[j], perm[pos]
				permute(pos + 1)
				perm[pos], perm[j] = perm[j], perm[pos]
			}
		}
		permute(0)

		// Concurrent execution on the real store: all batches start together
		// and touch heavily overlapping instances.
		for round := 0; round < 8; round++ {
			s := bootStore(w)
			start := make(chan struct{})
			var wg sync.WaitGroup
			verdicts := make(map[string]FailureKind)
			var mu sync.Mutex
			for idx := range w.batches {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					<-start
					_, err := s.ApplyBatch(w.batches[idx])
					kind := FailureNone
					if err != nil {
						be := &BatchError{}
						if asBatch(err, &be) {
							kind = be.Kind
						}
					}
					mu.Lock()
					verdicts[w.batches[idx].ClientID] = kind
					mu.Unlock()
				}(idx)
			}
			close(start)
			wg.Wait()

			got := outcomeKey(verdicts, storeDigest(s))
			if !expected[got] {
				t.Fatalf("trial %d round %d: concurrent outcome matches no serial order:\n%s\n\npossible outcomes: %d",
					trial, round, got, len(expected))
			}
		}

		// Direct parity check: each permutation on the real store must match
		// the naive model exactly for that same order.
		e := bootNaive(w)
		s := bootStore(w)
		naiveVerdicts := map[string]FailureKind{}
		storeVerdicts := map[string]FailureKind{}
		order := rng.Perm(n)
		for _, idx := range order {
			nv := e.apply(w.batches[idx])
			naiveVerdicts[w.batches[idx].ClientID] = nv.kind
			_, err := s.ApplyBatch(w.batches[idx])
			kind := FailureNone
			if err != nil {
				be := &BatchError{}
				if asBatch(err, &be) {
					kind = be.Kind
				}
			}
			storeVerdicts[w.batches[idx].ClientID] = kind
		}
		if e.digest() != storeDigest(s) {
			t.Fatalf("trial %d: same-order state divergence\nnaive: %s\nstore: %s",
				trial, e.digest(), storeDigest(s))
		}
		if outcomeKey(naiveVerdicts, "") != outcomeKey(storeVerdicts, "") {
			t.Fatalf("trial %d: verdict divergence naive=%v store=%v", trial, naiveVerdicts, storeVerdicts)
		}
	}
}

func asBatch(err error, target **BatchError) bool {
	if be, ok := err.(*BatchError); ok {
		*target = be
		return true
	}
	return false
}

func TestJournalReplayReproducesFinalState(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	w := genWorld(rng)
	s := bootStore(w)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for idx := range w.batches {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, _ = s.ApplyBatch(w.batches[idx])
		}(idx)
	}
	close(start)
	wg.Wait()

	digestAfter := storeDigest(s)

	// Replay every recorded attempt from scratch using the journal.
	replay := NewStore(w.reg)
	committedInJournal := 0
	for _, rec := range s.Journal() {
		_, err := replay.ApplyBatch(rec.Input)
		if rec.Committed {
			committedInJournal++
			if err != nil {
				t.Fatalf("journal replay: committed record now rejected: %v", err)
			}
		} else if err == nil {
			t.Fatalf("journal replay: rejected record now committed")
		}
	}
	if storeDigest(replay) != digestAfter {
		t.Fatalf("replayed state diverged\nlive:   %s\nreplay: %s", digestAfter, storeDigest(replay))
	}
	if replay.CommitSeq() != int64(committedInJournal) {
		t.Fatalf("commit clock = %d, journal commits = %d", replay.CommitSeq(), committedInJournal)
	}
}
