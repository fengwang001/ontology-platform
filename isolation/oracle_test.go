package isolation

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// This file cross-checks the isolator against a naive recursive
// simulation transcribed directly from the specification, over
// randomized batches, poison layouts, and transient-error scripts.

// sinkLog records every sink interaction of one Submit.
type sinkLog struct {
	calls   [][]string // every batch passed to the sink, in order
	success [][]string // batches the sink acknowledged
}

func hashBatch(seed uint64, ids []string) uint64 {
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	_, _ = h.Write(b[:])
	for _, id := range ids {
		_, _ = h.Write([]byte(id))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// newScriptedSink builds a deterministic sink: a batch containing a
// poison id always fails permanently; a clean batch fails transiently
// t times before succeeding, where t = hash(seed, batch) mod (r+2).
// t == r+1 means the batch never succeeds (transient exhaustion).
func newScriptedSink(poison map[string]bool, seed uint64, r int, log *sinkLog) WriteFunc {
	attempts := map[string]int{}
	return func(ids []string) error {
		cp := append([]string(nil), ids...)
		log.calls = append(log.calls, cp)
		for _, id := range ids {
			if poison[id] {
				return errPermanent
			}
		}
		key := strings.Join(ids, "\x00")
		t := int(hashBatch(seed, ids) % uint64(r+2))
		attempts[key]++
		if attempts[key] <= t {
			return fmt.Errorf("scripted: %w", ErrTransient)
		}
		log.success = append(log.success, cp)
		return nil
	}
}

// naiveSim is a straightforward transcription of the specification,
// used as an oracle. It deliberately mirrors the spec's structure
// (recursive solve returning verdict lists) rather than the
// implementation's.
type naiveSim struct {
	r, km, cmax int
	known       map[string]bool
	order       []string
}

type naiveState struct {
	r, cmax int
	sink    WriteFunc
	calls   int
	stop    bool
	poisons []string
}

func budgetDead(batch []string) []DeadLetter {
	out := make([]DeadLetter, len(batch))
	for i, id := range batch {
		out[i] = DeadLetter{ID: id, Reason: ReasonBudget}
	}
	return out
}

func (st *naiveState) tryBatch(batch []string) (ok, perm bool) {
	for attempt := 0; attempt <= st.r; attempt++ {
		if st.calls >= st.cmax {
			st.stop = true
			return false, false
		}
		st.calls++
		err := st.sink(batch)
		if err == nil {
			return true, false
		}
		if !errors.Is(err, ErrTransient) {
			return false, true
		}
	}
	return false, false
}

// solve returns (whole-batch-delivered, delivered ids, dead letters).
func (st *naiveState) solve(batch []string, certain bool) (bool, []string, []DeadLetter) {
	if len(batch) == 0 {
		return true, nil, nil
	}
	if st.stop {
		return false, nil, budgetDead(batch)
	}
	perm := certain
	if !certain {
		ok, p := st.tryBatch(batch)
		if ok {
			return true, append([]string(nil), batch...), nil
		}
		perm = p
		if st.stop {
			return false, nil, budgetDead(batch)
		}
	}
	if len(batch) == 1 {
		if perm {
			st.poisons = append(st.poisons, batch[0])
			return false, nil, []DeadLetter{{ID: batch[0], Reason: ReasonPoison}}
		}
		return false, nil, []DeadLetter{{ID: batch[0], Reason: ReasonExhausted}}
	}
	mid := (len(batch) + 1) / 2
	lok, ldel, ldead := st.solve(batch[:mid], false)
	_, rdel, rdead := st.solve(batch[mid:], perm && lok)
	return false, append(ldel, rdel...), append(ldead, rdead...)
}

func (sim *naiveSim) record(id string) {
	if sim.km == 0 || sim.known[id] {
		return
	}
	for len(sim.order) >= sim.km {
		delete(sim.known, sim.order[0])
		sim.order = sim.order[1:]
	}
	sim.known[id] = true
	sim.order = append(sim.order, id)
}

func (sim *naiveSim) submit(ids []string, sink WriteFunc) Result {
	snap := map[string]bool{}
	for id := range sim.known {
		snap[id] = true
	}
	st := &naiveState{r: sim.r, cmax: sim.cmax, sink: sink}
	var batch []string
	verdict := map[string]DeadLetter{}
	for _, id := range ids {
		if snap[id] {
			verdict[id] = DeadLetter{ID: id, Reason: ReasonKnown}
		} else {
			batch = append(batch, id)
		}
	}
	var delivered []string
	if len(batch) > 0 {
		var dl []DeadLetter
		_, delivered, dl = st.solve(batch, false)
		for _, d := range dl {
			verdict[d.ID] = d
		}
	}
	var dead []DeadLetter
	for _, id := range ids { // dead letters in original input order
		if d, ok := verdict[id]; ok {
			dead = append(dead, d)
		}
	}
	for _, p := range st.poisons { // merged at end, in determination order
		sim.record(p)
	}
	return Result{Delivered: delivered, Dead: dead, Calls: st.calls}
}

// checkInvariants verifies the per-Submit contractual invariants.
func checkInvariants(t *testing.T, ids []string, res Result, log *sinkLog) {
	t.Helper()

	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	// Delivered and dead are disjoint and their union is the input.
	seen := map[string]string{}
	for _, id := range res.Delivered {
		if _, dup := seen[id]; dup {
			t.Fatalf("id %q appears twice across results", id)
		}
		seen[id] = "delivered"
	}
	for _, d := range res.Dead {
		if _, dup := seen[d.ID]; dup {
			t.Fatalf("id %q appears twice across results", d.ID)
		}
		seen[d.ID] = "dead"
	}
	if len(seen) != len(ids) {
		t.Fatalf("delivered+dead = %d ids, input has %d", len(seen), len(ids))
	}
	// Both lists preserve the original order.
	last := -1
	for _, id := range res.Delivered {
		if pos[id] < last {
			t.Fatalf("Delivered out of order at %q", id)
		}
		last = pos[id]
	}
	last = -1
	for _, d := range res.Dead {
		if pos[d.ID] < last {
			t.Fatalf("Dead out of order at %q", d.ID)
		}
		last = pos[d.ID]
	}
	// Every delivered id appears in exactly one successful sink call.
	cnt := map[string]int{}
	for _, b := range log.success {
		for _, id := range b {
			cnt[id]++
		}
	}
	for _, id := range res.Delivered {
		if cnt[id] != 1 {
			t.Fatalf("delivered id %q in %d successful calls, want 1", id, cnt[id])
		}
	}
	// Poison-verdict ids never appear in a successful call.
	for _, d := range res.Dead {
		if d.Reason == ReasonPoison && cnt[d.ID] != 0 {
			t.Fatalf("poison id %q appeared in a successful call", d.ID)
		}
	}
	// Calls matches the number of sink invocations.
	if res.Calls != len(log.calls) {
		t.Fatalf("Calls = %d, sink saw %d calls", res.Calls, len(log.calls))
	}
}

func summarize(ids []string) string {
	if len(ids) <= 24 {
		return fmt.Sprintf("%v", ids)
	}
	return fmt.Sprintf("%v...(%d ids, fnv=%x)", ids[:8], len(ids), hashBatch(0, ids))
}

// TestRandomAgainstNaive replays 2000 randomized scenarios (batch
// sizes, poison layouts, transient scripts, budgets, table capacities)
// through both the isolator and the naive oracle and requires identical
// results, call sequences, and known tables.
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1177))
	for tc := 0; tc < 2000; tc++ {
		r := rng.Intn(4)  // 0..3 retries
		km := rng.Intn(6) // 0..5 table capacity
		cmax := 1_000_000
		if rng.Intn(4) == 0 {
			cmax = 1 + rng.Intn(10) // tight budget exercises Budget verdicts
		}
		seed := uint64(rng.Int63())

		var sinkFn WriteFunc
		iso, err := New(r, km, cmax, func(ids []string) error { return sinkFn(ids) })
		if err != nil {
			t.Fatalf("case %d: New: %v", tc, err)
		}
		sim := &naiveSim{r: r, km: km, cmax: cmax, known: map[string]bool{}}

		nSubmits := 1 + rng.Intn(3)
		for su := 0; su < nSubmits; su++ {
			n := 1 + rng.Intn(48)
			if rng.Intn(25) == 0 {
				n = 200 + rng.Intn(800)
			}
			ids := make([]string, n)
			for i := range ids {
				ids[i] = fmt.Sprintf("tc%d-s%d-i%d", tc, su, i)
			}
			poison := map[string]bool{}
			for _, id := range ids {
				if rng.Intn(6) == 0 {
					poison[id] = true
				}
			}

			logA := &sinkLog{}
			sinkFn = newScriptedSink(poison, seed, r, logA)
			resA, err := iso.Submit(ids)
			if err != nil {
				t.Fatalf("case %d submit %d: %v", tc, su, err)
			}

			logB := &sinkLog{}
			resB := sim.submit(ids, newScriptedSink(poison, seed, r, logB))

			match := reflect.DeepEqual(resA.Delivered, resB.Delivered) ||
				len(resA.Delivered) == 0 && len(resB.Delivered) == 0
			match = match && (reflect.DeepEqual(resA.Dead, resB.Dead) ||
				len(resA.Dead) == 0 && len(resB.Dead) == 0)
			match = match && resA.Calls == resB.Calls &&
				reflect.DeepEqual(logA.calls, logB.calls)
			if !match {
				t.Fatalf("case %d submit %d mismatch:\n R=%d Km=%d Cmax=%d n=%d poison=%v\n"+
					" isolator: delivered=%v dead=%v calls=%d\n"+
					" naive:    delivered=%v dead=%v calls=%d",
					tc, su, r, km, cmax, n, summarizeMap(poison),
					summarize(resA.Delivered), resA.Dead, resA.Calls,
					summarize(resB.Delivered), resB.Dead, resB.Calls)
			}
			checkInvariants(t, ids, resA, logA)

			t.Logf("case=%d submit=%d R=%d Km=%d Cmax=%d n=%d poisons=%s",
				tc, su, r, km, cmax, n, summarizeMap(poison))
			t.Logf("  input: %s", summarize(ids))
			t.Logf("  output: delivered=%s dead=%v calls=%d",
				summarize(resA.Delivered), resA.Dead, resA.Calls)
			t.Logf("  rationale: sink-batches=%s", summarizeBatches(logA.calls))
		}

		if got, want := iso.Known(), sim.order; !reflect.DeepEqual(got, want) &&
			!(len(got) == 0 && len(want) == 0) {
			t.Fatalf("case %d: Known() = %v, naive table = %v", tc, got, want)
		}
		t.Logf("case=%d final known table: %v", tc, iso.Known())
		if err := iso.Close(); err != nil {
			t.Fatalf("case %d: Close: %v", tc, err)
		}
	}
}

func summarizeMap(m map[string]bool) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if len(keys) <= 12 {
		return fmt.Sprintf("%v", keys)
	}
	return fmt.Sprintf("%v...(%d)", keys[:6], len(keys))
}

func summarizeBatches(batches [][]string) string {
	if len(batches) <= 12 {
		parts := make([]string, len(batches))
		for i, b := range batches {
			parts[i] = fmt.Sprintf("[%s]", strings.Join(b, " "))
		}
		return strings.Join(parts, " ")
	}
	return fmt.Sprintf("%d batches", len(batches))
}

// TestConcurrentStress hammers the isolator with concurrent Submits and
// Known queries (run with -race). The sink is thread-safe and
// deterministic per batch content; every Submit result must satisfy the
// contractual invariants, and the table must never exceed Km or hold
// duplicates.
func TestConcurrentStress(t *testing.T) {
	const r, km, cmax = 2, 8, 1_000_000
	poison := map[string]bool{"p0": true, "p1": true, "p2": true}

	var mu sync.Mutex
	attempts := map[string]int{}
	sink := func(batch []string) error {
		mu.Lock()
		defer mu.Unlock()
		for _, id := range batch {
			if poison[id] {
				return errPermanent
			}
		}
		key := strings.Join(batch, "\x00")
		attempts[key]++
		if attempts[key] <= int(hashBatch(7, batch)%uint64(r+2)) {
			return ErrTransient
		}
		return nil
	}
	iso := mustNew(t, r, km, cmax, sink)

	rng := rand.New(rand.NewSource(9))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for it := 0; it < 25; it++ {
				mu.Lock()
				n := 1 + rng.Intn(12)
				batch := make([]string, n)
				for i := range batch {
					if rng.Intn(4) == 0 {
						batch[i] = fmt.Sprintf("p%d", rng.Intn(3))
					} else {
						batch[i] = fmt.Sprintf("g%d-it%d-i%d", g, it, i)
					}
				}
				// dedupe within the batch
				seen := map[string]bool{}
				out := batch[:0]
				for _, id := range batch {
					if !seen[id] {
						seen[id] = true
						out = append(out, id)
					}
				}
				batch = append([]string(nil), out...)
				mu.Unlock()

				if len(batch) == 0 {
					continue
				}
				res, err := iso.Submit(batch)
				if err != nil && !errors.Is(err, ErrClosed) {
					t.Errorf("Submit: %v", err)
					continue
				}
				if err == nil {
					seen2 := map[string]bool{}
					for _, id := range res.Delivered {
						if seen2[id] {
							t.Errorf("id %q twice in results", id)
						}
						seen2[id] = true
					}
					for _, d := range res.Dead {
						if seen2[d.ID] {
							t.Errorf("id %q twice in results", d.ID)
						}
						seen2[d.ID] = true
					}
					if len(seen2) != len(batch) {
						t.Errorf("result covers %d ids, input had %d", len(seen2), len(batch))
					}
				}
				_ = iso.Known()
			}
		}(g)
	}
	wg.Wait()
	if err := iso.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	known := iso.Known()
	if len(known) > km {
		t.Fatalf("Known() has %d entries, capacity %d", len(known), km)
	}
	seen := map[string]bool{}
	for _, id := range known {
		if seen[id] {
			t.Fatalf("Known() contains duplicate %q", id)
		}
		seen[id] = true
	}
}
