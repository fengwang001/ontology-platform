package bulkimport_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/bulkimport"
	"ontology/bulkimport/naive"
)

// event is one step of a randomized import scenario.
type event struct {
	kind  string // "submit" or "close"
	chunk bulkimport.Chunk
	note  string
}

// generateScenario builds a random job: entries with random cross-chunk
// references (some dangling forever), random validation failures, a
// random arrival order, injected duplicates (identical and
// conflicting), and an optional early close.
func generateScenario(r *rand.Rand, jobID string) (cfg bulkimport.JobConfig, events []event, allIDs []string) {
	numChunks := 1 + r.Intn(6)
	numEntries := 1 + r.Intn(24)

	idPool := make([]string, 0, numEntries+2)
	for i := 0; i < numEntries; i++ {
		idPool = append(idPool, fmt.Sprintf("e%02d", i))
	}
	ghosts := []string{"ghost0", "ghost1"}

	chunks := make([][]bulkimport.Entry, numChunks)
	for i := 0; i < numEntries; i++ {
		e := bulkimport.Entry{ID: idPool[i], Fields: map[string]string{"name": idPool[i]}}
		if r.Intn(6) == 0 {
			e.Fields["bad"] = "1"
		}
		for k := 0; k < r.Intn(4); k++ {
			if r.Intn(5) == 0 {
				e.Refs = append(e.Refs, ghosts[r.Intn(len(ghosts))])
			} else {
				e.Refs = append(e.Refs, idPool[r.Intn(len(idPool))])
			}
		}
		seq := r.Intn(numChunks)
		chunks[seq] = append(chunks[seq], e)
	}

	maxChunks := numChunks
	if r.Intn(5) == 0 {
		maxChunks = 0 // no declared limit
	}
	cfg = bulkimport.JobConfig{ID: jobID, MaxChunks: maxChunks, Validator: badEntryValidator}

	for seq := range chunks {
		events = append(events, event{kind: "submit",
			chunk: bulkimport.Chunk{JobID: jobID, Seq: seq, Entries: chunks[seq]},
			note:  "original"})
	}
	// Inject duplicate deliveries.
	for seq := range chunks {
		for d := 0; d < r.Intn(3); d++ {
			dup := bulkimport.Chunk{JobID: jobID, Seq: seq}
			dup.Entries = append(dup.Entries, chunks[seq]...)
			note := "identical-duplicate"
			if r.Intn(2) == 0 {
				// Conflicting redelivery: mutate the content.
				note = "conflicting-duplicate"
				switch r.Intn(3) {
				case 0:
					dup.Entries = append(dup.Entries, bulkimport.Entry{
						ID:     fmt.Sprintf("cx%d", seq),
						Fields: map[string]string{"name": "cx"},
					})
				case 1:
					if len(dup.Entries) > 0 {
						// Copy before mutating: Refs slices are shared
						// with the original chunk's entries.
						refs := append([]string{}, dup.Entries[0].Refs...)
						dup.Entries[0].Refs = append(refs, fmt.Sprintf("gx%d", seq))
					} else {
						dup.Entries = append(dup.Entries, bulkimport.Entry{
							ID:     fmt.Sprintf("cx%d", seq),
							Fields: map[string]string{"name": "cx"},
						})
					}
				default:
					if len(dup.Entries) > 0 {
						dup.Entries[0].Fields = map[string]string{"name": "mutated"}
					} else {
						dup.Entries = append(dup.Entries, bulkimport.Entry{
							ID:     fmt.Sprintf("cx%d", seq),
							Fields: map[string]string{"name": "cx"},
						})
					}
				}
			}
			events = append(events, event{kind: "submit", chunk: dup, note: note})
		}
	}
	if r.Intn(10) < 3 {
		events = append(events, event{kind: "close", note: "early-close"})
	}
	// Shuffle the arrival order.
	r.Shuffle(len(events), func(i, j int) { events[i], events[j] = events[j], events[i] })

	idSet := map[string]bool{}
	for _, ev := range events {
		for _, e := range ev.chunk.Entries {
			idSet[e.ID] = true
		}
	}
	for id := range idSet {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)
	return cfg, events, allIDs
}

func outcomeKey(res bulkimport.ChunkResult) string {
	if res.Outcome == bulkimport.ChunkRejected && res.Err != nil {
		return fmt.Sprintf("%s/%s", res.Outcome, res.Err.Kind)
	}
	return res.Outcome.String()
}

func viewKey(v bulkimport.EntryView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", v.Status)
	if v.Err != nil {
		// Note: the exact RefID attribution of a propagated failure is
		// engine-specific (the incremental engine reports the ref that
		// failed first in arrival time; the naive model reports the
		// smallest failed ref id). Both are deterministic, so only the
		// error category is compared here.
		fmt.Fprintf(&b, "/%s", v.Err.Kind)
	}
	for _, w := range v.WaitingOn {
		fmt.Fprintf(&b, "/wait:%s:%s", w.TargetID, w.Reason)
	}
	return b.String()
}

// TestDifferentialRandom drives the incremental engine and the naive
// wait-for-everything model with identical random event sequences and
// requires identical chunk verdicts and identical final entry states.
func TestDifferentialRandom(t *testing.T) {
	const seeds = 300
	for seed := int64(0); seed < seeds; seed++ {
		r := rand.New(rand.NewSource(seed))
		cfg, events, allIDs := generateScenario(r, "jd")

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		m := bulkimport.NewManager(bulkimport.WithLogger(logger))
		if err := m.CreateJob(cfg); err != nil {
			t.Fatalf("seed %d: CreateJob: %v", seed, err)
		}
		nm := naive.New(cfg.MaxChunks, cfg.Validator)

		var trace strings.Builder
		failed := false
		for i, ev := range events {
			switch ev.kind {
			case "close":
				if err := m.CloseJob(cfg.ID); err != nil {
					t.Fatalf("seed %d: CloseJob: %v", seed, err)
				}
				nm.Close()
				fmt.Fprintf(&trace, "event %d: close\n", i)
			case "submit":
				got, err := m.SubmitChunk(ev.chunk)
				if err != nil {
					t.Fatalf("seed %d: SubmitChunk: %v", seed, err)
				}
				want := nm.SubmitChunk(ev.chunk)
				fmt.Fprintf(&trace, "event %d: submit seq=%d entries=%d (%s) -> engine=%s naive=%s\n",
					i, ev.chunk.Seq, len(ev.chunk.Entries), ev.note, outcomeKey(got), outcomeKey(want))
				if outcomeKey(got) != outcomeKey(want) {
					t.Errorf("seed %d event %d (%s seq=%d): chunk verdict engine=%s naive=%s\n"+
						"chunk=%+v\ntrace:\n%s\nengine log:\n%s",
						seed, i, ev.note, ev.chunk.Seq, outcomeKey(got), outcomeKey(want),
						ev.chunk, trace.String(), logBuf.String())
					failed = true
				}
			}
			if failed {
				break
			}
		}
		if failed {
			continue
		}

		nm.Finalize()
		for _, id := range allIDs {
			got, err := m.QueryEntry(cfg.ID, id)
			if err != nil {
				t.Fatalf("seed %d: QueryEntry(%q): %v", seed, id, err)
			}
			want := nm.QueryEntry(id)
			if viewKey(got) != viewKey(want) {
				t.Errorf("seed %d: entry %q: engine=%s naive=%s\ntrace:\n%s\nengine log:\n%s",
					seed, id, viewKey(got), viewKey(want), trace.String(), logBuf.String())
			}
		}
		// Ghost references must be unknown in both.
		for _, g := range []string{"ghost0", "ghost1"} {
			got, _ := m.QueryEntry(cfg.ID, g)
			want := nm.QueryEntry(g)
			if got.Status != want.Status {
				t.Errorf("seed %d: ghost %q: engine=%v naive=%v", seed, g, got.Status, want.Status)
			}
		}
		if t.Failed() {
			return
		}
	}
}

// TestDeterministicReplay runs identical scenarios twice and requires
// bit-identical outcomes from the incremental engine.
func TestDeterministicReplay(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		run := func() map[string]string {
			r := rand.New(rand.NewSource(seed))
			cfg, events, allIDs := generateScenario(r, "jr")
			m := bulkimport.NewManager()
			if err := m.CreateJob(cfg); err != nil {
				t.Fatalf("CreateJob: %v", err)
			}
			for _, ev := range events {
				if ev.kind == "close" {
					_ = m.CloseJob(cfg.ID)
					continue
				}
				if _, err := m.SubmitChunk(ev.chunk); err != nil {
					t.Fatalf("SubmitChunk: %v", err)
				}
			}
			out := map[string]string{}
			for _, id := range allIDs {
				v, err := m.QueryEntry(cfg.ID, id)
				if err != nil {
					t.Fatalf("QueryEntry: %v", err)
				}
				out[id] = viewKey(v)
			}
			return out
		}
		first, second := run(), run()
		for id, v := range first {
			if second[id] != v {
				t.Fatalf("seed %d entry %q: nondeterministic result %q vs %q", seed, id, v, second[id])
			}
		}
	}
}
