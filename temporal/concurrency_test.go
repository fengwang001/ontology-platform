package temporal

import (
	"sync"
	"testing"
)

// TestConcurrentWritesDuringTraversal runs many traversals pinned at an old
// instant while writer goroutines continuously create objects/links, migrate
// property definitions and adjust cardinality. Every traversal must return
// exactly the oracle state at its fixed baseline, independently of when its
// BFS physically happens; results of identical requests must be identical.
func TestConcurrentWritesDuringTraversal(t *testing.T) {
	s := NewStore()

	// Baseline graph: chain plus a star, committed by instant 5.
	tx := s.Begin()
	tx.CreateObjectType("T", []Property{{Name: "v", Type: "int"}})
	tx.CreateLinkType("e", Cardinality{MaxOut: -1})
	mustCommit(t, tx)

	// objects n0..n9 and chain links n_i -> n_{i+1}
	for i := 0; i < 10; i++ {
		tx = s.Begin()
		tx.CreateObject(ObjectID("n"+itoa(i)), "T", PropertyValues{"v": i})
		if i > 0 {
			if err := tx.CreateLink("e", ObjectID("n"+itoa(i-1)), ObjectID("n"+itoa(i))); err != nil {
				t.Fatal(err)
			}
		}
		mustCommit(t, tx)
	}
	baseline := s.Head() // 11

	// Expected oracle result at baseline.
	oracle := NewNaiveModel()
	oracle.IngestLog(s.LogEntries())
	cfg := TraversalConfig{Start: "n0", At: baseline,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}
	want := oracle.NaiveTraverse(cfg)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers: churn new objects/links, migrations, cardinality changes.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				tx := s.Begin()
				obj := ObjectID("w" + itoa(id) + "_" + itoa(i))
				tx.CreateObject(obj, "T", PropertyValues{"v": i})
				// links are unbounded at creation; cardinality may be
				// tightened by another writer, in which case the commit
				// is rejected and that is fine.
				_ = tx.CreateLink("e", "n0", obj)
				if i%7 == 0 {
					tx.MigrateObjectType("T", []Property{
						{Name: "v", Type: "int"},
						{Name: "gen" + itoa(i), Type: "int"},
					})
				}
				if i%5 == 0 {
					tx.AdjustCardinality("e", Cardinality{MaxOut: []int{-1, 5000, 1, 10000}[i%4]})
				}
				_, _ = tx.Commit()
				i++
			}
		}(w)
	}

	// Readers: repeatedly traverse the fixed baseline and compare every time.
	const readers = 8
	const perReader = 60
	var readerWG sync.WaitGroup
	var readerErrMu sync.Mutex
	var readerErr error
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for k := 0; k < perReader; k++ {
				got, err := s.Traverse(cfg, nil)
				if err != nil {
					readerErrMu.Lock()
					readerErr = err
					readerErrMu.Unlock()
					return
				}
				if !deepResultEqual(got, want) {
					readerErrMu.Lock()
					readerErr = &TraversalError{Code: ErrMissingHistory, At: baseline,
						Detail: "concurrent traversal diverged from fixed baseline"}
					readerErrMu.Unlock()
					return
				}
			}
		}()
	}

	// While readers run, also perform serial traversals on the main goroutine.
	for k := 0; k < perReader; k++ {
		got, err := s.Traverse(cfg, nil)
		if err != nil {
			t.Fatalf("serial-during-writes failed: %v", err)
		}
		if !deepResultEqual(got, want) {
			t.Fatalf("serial-during-writes diverged from baseline")
		}
	}

	readerWG.Wait()
	close(stop)
	wg.Wait()
	if readerErr != nil {
		t.Fatalf("concurrent reader mismatch: %v", readerErr)
	}
}

// TestSnapshotSerializabilityAgainstNaive runs concurrent traversals at
// multiple baselines while writes continue, then re-ingests the final commit
// log into the oracle and verifies every result equals the oracle slice at
// its baseline: this is the "equivalent to some serial global order" claim.
func TestSnapshotSerializabilityAgainstNaive(t *testing.T) {
	s := NewStore()
	tx := s.Begin()
	tx.CreateObjectType("T", []Property{{Name: "v", Type: "int"}})
	tx.CreateLinkType("e", Cardinality{MaxOut: -1})
	mustCommit(t, tx)

	// Pre-populate through instant 4.
	for i := 0; i < 4; i++ {
		tx = s.Begin()
		tx.CreateObject(ObjectID("b"+itoa(i)), "T", PropertyValues{"v": i})
		if i > 0 {
			_ = tx.CreateLink("e", ObjectID("b"+itoa(i-1)), ObjectID("b"+itoa(i)))
		}
		mustCommit(t, tx)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tx := s.Begin()
			tx.CreateObject(ObjectID("c"+itoa(i)), "T", PropertyValues{"v": i})
			if i%3 == 0 {
				tx.AdjustCardinality("e", Cardinality{MaxOut: -1})
			}
			_, _ = tx.Commit()
		}
	}()

	// Capture baseline results at a range of past instants while writes fly.
	var mu sync.Mutex
	type captured struct {
		at  Instant
		res *TraversalResult
	}
	var capturedResults []captured
	var readerWG sync.WaitGroup
	for r := 0; r < 16; r++ {
		readerWG.Add(1)
		go func(r int) {
			defer readerWG.Done()
			at := Instant(2 + r%3) // baselines 2,3,4
			res, err := s.Traverse(TraversalConfig{Start: "b0", At: at,
				Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
			if err != nil {
				t.Errorf("baseline traverse @%d: %v", at, err)
				return
			}
			mu.Lock()
			capturedResults = append(capturedResults, captured{at: at, res: res})
			mu.Unlock()
		}(r)
	}
	readerWG.Wait()
	close(stop)
	wg.Wait()

	oracle := NewNaiveModel()
	oracle.IngestLog(s.LogEntries())
	for _, c := range capturedResults {
		want := oracle.NaiveTraverse(TraversalConfig{Start: "b0", At: c.at,
			Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}})
		if !deepResultEqual(c.res, want) {
			t.Fatalf("captured result @%d not serializable against oracle", c.at)
		}
	}
}

func deepResultEqual(a, b *TraversalResult) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(a.Objects) != len(b.Objects) || len(a.Links) != len(b.Links) {
		return false
	}
	for i := range a.Objects {
		x, y := a.Objects[i], b.Objects[i]
		if x.State.ID != y.State.ID || x.Depth != y.Depth {
			return false
		}
	}
	for i := range a.Links {
		if a.Links[i] != b.Links[i] {
			return false
		}
	}
	return true
}
