package naive_test

import (
	"bytes"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/naive"
	"ontology/ontology"
)

func diffConfig() ontology.Config {
	return ontology.Config{
		ObjectTypes: map[string]ontology.ObjectType{
			"Person": {Name: "Person"},
			"Org":    {Name: "Org"},
		},
		LinkTypes: map[string]ontology.LinkType{
			"member": {
				Name: "member", SourceType: "Person", TargetType: "Org",
				MaxSource: 2, MaxTarget: -1,
			},
			"owns": {
				Name: "owns", SourceType: "Person", TargetType: "Org",
				MaxSource: -1, MaxTarget: 1,
			},
		},
	}
}

type world struct {
	people   []ontology.ID
	orgs     []ontology.ID
	versions map[ontology.ID]uint64 // oracle-maintained current versions
	links    map[string]map[ontology.ID]map[ontology.ID]struct{}
}

func buildWorld(t *testing.T, seed int64, peopleN, orgsN int) (*world, *ontology.Store, *naive.Engine) {
	t.Helper()
	cfg := diffConfig()
	store := ontology.New(cfg)
	engine := naive.New(cfg)
	w := &world{
		versions: map[ontology.ID]uint64{},
		links: map[string]map[ontology.ID]map[ontology.ID]struct{}{
			"member": {},
			"owns":   {},
		},
	}
	for i := 0; i < peopleN; i++ {
		id := ontology.ID("p" + itoaDiff(i))
		if err := store.CreateInstance(id, "Person"); err != nil {
			t.Fatal(err)
		}
		if err := engine.CreateInstance(id, "Person"); err != nil {
			t.Fatal(err)
		}
		w.people = append(w.people, id)
		w.versions[id] = 1
		w.links["member"][id] = map[ontology.ID]struct{}{}
		w.links["owns"][id] = map[ontology.ID]struct{}{}
	}
	for i := 0; i < orgsN; i++ {
		id := ontology.ID("o" + itoaDiff(i))
		if err := store.CreateInstance(id, "Org"); err != nil {
			t.Fatal(err)
		}
		if err := engine.CreateInstance(id, "Org"); err != nil {
			t.Fatal(err)
		}
		w.orgs = append(w.orgs, id)
		w.versions[id] = 1
	}
	return w, store, engine
}

// genBatch creates a random small batch. Most preconditions reference the
// oracle-known current version (so commits are reachable), a minority are
// deliberately stale to exercise the mismatch path; duplicates are injected
// rarely to exercise gate 1.
func (w *world) genBatch(rng *rand.Rand, seq int) ontology.Batch {
	b := ontology.Batch{ID: "gen-" + itoaDiff(seedTag(rng)) + "-" + itoaDiff(seq)}

	touched := map[ontology.ID]bool{}
	pick := func() ontology.ID {
		if rng.Intn(2) == 0 && len(w.people) > 0 {
			return w.people[rng.Intn(len(w.people))]
		}
		return w.orgs[rng.Intn(len(w.orgs))]
	}

	pn := 1 + rng.Intn(3)
	for i := 0; i < pn; i++ {
		id := pick()
		if touched[id] {
			// Occasionally keep the duplicate (gate 1); otherwise re-roll.
			if rng.Intn(4) != 0 {
				i--
				continue
			}
		}
		touched[id] = true
		expected := w.versions[id]
		if rng.Intn(3) == 0 {
			expected = uint64(int(expected) + 1 + rng.Intn(3))
		}
		b.Preconditions = append(b.Preconditions, ontology.Precondition{Instance: id, ExpectedVersion: expected})
	}

	on := rng.Intn(3)
	for i := 0; i < on; i++ {
		if rng.Intn(2) == 0 {
			id := w.people[rng.Intn(len(w.people))]
			b.Ops = append(b.Ops, ontology.Op{
				Kind: ontology.OpSetAttr, Instance: id,
				Attr: ontology.Attr("a" + itoaDiff(rng.Intn(3))), Value: rng.Intn(1000),
			})
			continue
		}
		link := "member"
		if rng.Intn(2) == 0 {
			link = "owns"
		}
		kind := ontology.OpAddLink
		// Bias adds/removes against existing links so cardinality edges move.
		if rng.Intn(3) == 0 && len(w.links[link][w.people[0]]) >= 0 {
			kind = ontology.OpRemoveLink
		}
		p := w.people[rng.Intn(len(w.people))]
		o := w.orgs[rng.Intn(len(w.orgs))]
		b.Ops = append(b.Ops, ontology.Op{Kind: kind, Instance: p, LinkType: link, Other: o})
	}
	return b
}

func seedTag(rng *rand.Rand) int { return rng.Intn(1_000_000) }

func itoaDiff(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [24]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		buf[p] = '-'
	}
	return string(buf[p:])
}

func classify(r ontology.Result) ontology.Status { return r.Status }

type entry struct {
	batch ontology.Batch
	res   ontology.Result
}

// TestSerialDifferential runs the same generated sequence serially against
// both implementations and requires identical classifications and final
// state. This pins behavioural equivalence before introducing concurrency.
func TestSerialDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		w, store, engine := buildWorld(t, seed, 6+rng.Intn(6), 3+rng.Intn(4))
		for step := 0; step < 120; step++ {
			b := w.genBatch(rng, step)
			got := store.Commit(b)
			want := engine.Commit(b)
			if classify(got) != classify(want) {
				t.Fatalf("seed=%d step=%d batch=%+v\n got=%+v\nwant=%+v",
					seed, step, b, got, want)
			}
			if want.Status == ontology.StatusCommitted {
				w.applyCommitted(b)
			}
		}
		assertWorldsEqual(t, w, store, engine)
	}
}

func (w *world) applyCommitted(b ontology.Batch) {
	touched := map[ontology.ID]struct{}{}
	for _, p := range b.Preconditions {
		touched[p.Instance] = struct{}{}
	}
	for _, op := range b.Ops {
		touched[op.Instance] = struct{}{}
		if op.Kind == ontology.OpAddLink || op.Kind == ontology.OpRemoveLink {
			touched[op.Other] = struct{}{}
		}
	}
	for id := range touched {
		w.versions[id]++
	}
	for _, op := range b.Ops {
		switch op.Kind {
		case ontology.OpAddLink:
			w.links[op.LinkType][op.Instance][op.Other] = struct{}{}
		case ontology.OpRemoveLink:
			delete(w.links[op.LinkType][op.Instance], op.Other)
		}
	}
}

func assertWorldsEqual(t *testing.T, w *world, store *ontology.Store, engine *naive.Engine) {
	t.Helper()
	all := append(append([]ontology.ID{}, w.people...), w.orgs...)
	for _, id := range all {
		s1, ok1 := store.Get(id)
		s2, ok2 := engine.Get(id)
		if !ok1 || !ok2 {
			t.Fatalf("missing %s: %v %v", id, ok1, ok2)
		}
		if s1.Version != s2.Version {
			t.Fatalf("%s versions differ: store=%d naive=%d", id, s1.Version, s2.Version)
		}
		attrsEqual(t, id, s1.Attrs, s2.Attrs)
	}
}

func attrsEqual(t *testing.T, id ontology.ID, a, b map[ontology.Attr]ontology.Value) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s attr sizes %d vs %d", id, len(a), len(b))
	}
	for k, v := range a {
		if b[k] != v {
			t.Fatalf("%s attr %s: %v vs %v", id, k, v, b[k])
		}
	}
}

// TestConcurrentReplayEquivalence runs batches concurrently against the
// store, recording every (batch, result) in completion order, then replays
// that exact order through the serial naive oracle. A rejected batch must
// be rejected at the same position in both worlds; a committed batch must
// commit. This is the "equivalent to some serial order" check: completion
// order of a strict-2PL system is a valid linearisation order.
func TestConcurrentReplayEquivalence(t *testing.T) {
	for seed := int64(100); seed <= 120; seed++ {
		rng := rand.New(rand.NewSource(seed))
		w, store, replayEngine := buildWorld(t, seed, 8, 4)

		const total = 200
		batches := make([]ontology.Batch, total)
		for i := 0; i < total; i++ {
			b := w.genBatch(rng, i)
			batches[i] = b
		}

		entries := make([]entry, total)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range batches {
			wg.Add(1)
			go func(idx int, b ontology.Batch) {
				defer wg.Done()
				<-start
				r := store.Commit(b)
				entries[idx] = entry{b, r}
			}(i, batches[i])
		}
		close(start)
		wg.Wait()

		// The store assigns every decision a global linearisation Order at
		// the commit critical section. Replaying in that order reproduces a
		// valid serial execution of the concurrent history.
		sort.Slice(entries, func(i, j int) bool { return entries[i].res.AcquireOrder < entries[j].res.AcquireOrder })

		for i, e := range entries {
			serial := replayEngine.Commit(e.batch)
			if serial.Status != e.res.Status {
				t.Fatalf("seed=%d position=%d batch=%s: concurrent=%s serial=%s",
					seed, i, e.batch.ID, e.res.Status, serial.Status)
			}
			if serial.Status == ontology.StatusCommitted {
				compareCommits(t, serial, e.res)
			}
		}
		assertWorldsEqual(t, w, store, replayEngine)

		// The store journal must itself replay cleanly to the same state.
		var buf bytes.Buffer
		if _, err := store.Journal().WriteTo(&buf); err != nil {
			t.Fatal(err)
		}
		report, err := ontology.ReplayJournal(diffConfig(), bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("journal replay: %v", err)
		}
		if report.Committed+report.Rejected != total {
			t.Fatalf("report counts %+v", report)
		}
	}
}

func compareCommits(t *testing.T, serial, concurrent ontology.Result) {
	t.Helper()
	if len(serial.Versions) != len(concurrent.Versions) {
		t.Fatalf("version change sets %d vs %d", len(serial.Versions), len(concurrent.Versions))
	}
	for id, vc := range serial.Versions {
		got, ok := concurrent.Versions[id]
		if !ok || got.From != vc.From || got.To != vc.To {
			t.Fatalf("version change %s: concurrent=%+v serial=%+v", id, got, vc)
		}
	}
}
