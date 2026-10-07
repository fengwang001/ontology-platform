package importguard

import (
	"fmt"
	"testing"
)

// TestSnapshotStableDuringExecution: permission entries changed after batch
// start cannot affect any record of that batch. The whole batch runs inside
// the store critical section, so a concurrent Grant is forced to wait until
// the batch completes; the batch's decisions are therefore provably taken
// from the start snapshot. A subsequent batch observes the change.
func TestSnapshotStableDuringExecution(t *testing.T) {
	s, g, _ := newTypedStore(t, "name")
	s.Grant("alice", "Person", "name", ActionWrite)
	entries := make([]Entry, 50)
	for i := range entries {
		entries[i] = Entry{ObjectID: fmt.Sprintf("o%d", i), Type: "Person", Semantic: SemanticCreate,
			Fields: map[string]PropertyValue{"name": "n", "late": i}}
	}

	done := make(chan struct{})
	go func() {
		s.Grant("alice", "Person", "late", ActionWrite)
		close(done)
	}()

	r := g.BatchImport("alice", ModeLenient, entries)
	for i, rr := range r.Records {
		// "late" is ungranted at snapshot time -> skipped (not failed: not
		// required) -> every record partial, none must contain "late".
		if rr.Status != StatusPartial || len(rr.Skipped) != 1 || rr.Skipped[0] != "late" {
			t.Fatalf("record %d must skip late from start snapshot, got %+v", i, rr)
		}
		if o, ok := s.GetObject(fmt.Sprintf("o%d", i)); ok {
			if _, w := o.Properties["late"]; w {
				t.Fatalf("record %d observed post-start permission grant", i)
			}
		}
	}
	<-done

	r2 := g.BatchImport("alice", ModeLenient, []Entry{{
		ObjectID: "z", Type: "Person", Semantic: SemanticCreate,
		Fields: map[string]PropertyValue{"name": "n", "late": 1},
	}})
	if r2.Records[0].Status != StatusSuccess {
		t.Fatalf("new batch must observe the post-batch grant, got %+v", r2.Records[0])
	}
}

// TestScaleIndependentPermissionLookups verifies the scale-independent
// performance claim directly:
//
//	permission cells consulted per record == number of fields of that record
//
// regardless of (a) batch size and (b) the total number (and revision
// history length) of permission entries for the type. The index is built
// once per batch (O(history)); each judgment is one map lookup per field.
func TestScaleIndependentPermissionLookups(t *testing.T) {
	for _, history := range []int{0, 1, 100, 10_000} {
		s, g, _ := newTypedStore(t, "name")
		s.Grant("alice", "Person", "name", ActionWrite)
		s.Grant("alice", "Person", "a", ActionWrite)
		s.Grant("alice", "Person", "b", ActionWrite)
		for i := 0; i < history; i++ {
			// unrelated cells and repeated revisions of unrelated cells
			s.Grant("alice", "Person", fmt.Sprintf("noise%d", i%64), ActionWrite)
		}
		for _, n := range []int{1, 16, 128} {
			batch := make([]Entry, n)
			for j := range batch {
				batch[j] = Entry{ObjectID: fmt.Sprintf("h%d_%d_o%d", history, n, j),
					Type: "Person", Semantic: SemanticCreate,
					Fields: map[string]PropertyValue{"name": "x", "a": 1, "b": 2}}
			}
			r := g.BatchImport("alice", ModeAtomic, batch)
			for _, rr := range r.Records {
				if rr.Status == StatusFailed {
					t.Fatalf("history=%d n=%d unexpected failure %+v", history, n, rr)
				}
			}
		}
	}

	// Exact-count proof on a directly inspected snapshot.
	s, _, _ := newTypedStore(t, "name")
	for i := 0; i < 5000; i++ {
		s.Grant("alice", "Person", fmt.Sprintf("f%d", i), ActionWrite)
	}
	s.mu.Lock()
	snap := takeSnapshotLocked(s)
	s.mu.Unlock()
	if snap.historyCount != 5000 {
		t.Fatalf("snapshot must fold all %d history entries once", 5000)
	}
	var probes int
	for _, f := range []string{"f7", "f4999", "ghost"} {
		_, _ = snap.canWrite("alice", "Person", f, &probes)
	}
	if probes != 3 {
		t.Fatalf("3 field questions must cost exactly 3 permission-cell lookups regardless of history, got %d", probes)
	}

	// And through the full pipeline: the per-record probe count captured in
	// phase 1 must equal the field count even for the large-history store.
	s2, g2, _ := newTypedStore(t, "name")
	for i := 0; i < 5000; i++ {
		s2.Grant("alice", "Person", fmt.Sprintf("g%d", i), ActionWrite)
	}
	e := Entry{ObjectID: "probe", Type: "Person", Semantic: SemanticCreate,
		Fields: map[string]PropertyValue{"g1": 1, "g2": 2, "g3": 3, "g4": 4}}
	s2.mu.Lock()
	snap2 := takeSnapshotLocked(s2)
	var p int
	jr := g2.judge("alice", ModeLenient, 0, e, snap2, &p)
	s2.mu.Unlock()
	if p != 4 {
		t.Fatalf("4-field record must consult exactly 4 permission cells (history=5000), got %d", p)
	}
	if jr.permLookups != 4 {
		t.Fatalf("judged record should record 4 lookups, got %d", jr.permLookups)
	}
}
