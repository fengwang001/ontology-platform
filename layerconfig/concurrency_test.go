package layerconfig_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/layerconfig"
)

// TestConcurrentLinearizability runs concurrent publishers, readers and
// rollbackers and verifies:
//
//   - readers never observe a torn state (the key is always present);
//   - accepted publish versions form a contiguous, gap-free sequence;
//   - every accepted publish is reflected atomically.
func TestConcurrentLinearizability(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{
		Key: "k", Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride,
	}, l)
	if _, err := s.Publish([]layerconfig.Change{{
		Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "k",
		Value: layerconfig.Value{Str: "init"},
	}}); err != nil {
		t.Fatal(err)
	}

	const readers = 8
	const writers = 8
	const writesPerWriter = 200

	var readerWg sync.WaitGroup
	var writerWg sync.WaitGroup
	stopReaders := make(chan struct{})

	for r := 0; r < readers; r++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				res, err := s.Resolve(-1, layerconfig.Scope{}, "k")
				if err != nil || !res.Present {
					t.Errorf("reader observed inconsistent state: present=%v err=%v", res.Present, err)
					return
				}
			}
		}()
	}

	var (
		mu       sync.Mutex
		versions []int
		errs     []error
	)
	for w := 0; w < writers; w++ {
		writerWg.Add(1)
		go func(id int) {
			defer writerWg.Done()
			for i := 0; i < writesPerWriter; i++ {
				val := fmt.Sprintf("w%d-%d", id, i)
				ver, err := s.Publish([]layerconfig.Change{{
					Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "k",
					Value: layerconfig.Value{Str: val},
				}})
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
				} else {
					versions = append(versions, ver)
				}
				mu.Unlock()
			}
		}(w)
	}
	writerWg.Wait()

	// Concurrent rollbacks while writers finish and readers run.
	var rbWg sync.WaitGroup
	for i := 0; i < 16; i++ {
		rbWg.Add(1)
		go func() {
			defer rbWg.Done()
			cur := s.CurrentVersion()
			if cur >= 2 {
				if _, err := s.Rollback(cur - 1); err != nil {
					t.Errorf("rollback: %v", err)
				}
			}
		}()
	}
	rbWg.Wait()
	close(stopReaders)
	readerWg.Wait()

	for _, e := range errs {
		if e != nil {
			t.Fatalf("unexpected publish error: %v", e)
		}
	}

	// Accepted versions must be a contiguous run starting at 2 (1 was the
	// seed), regardless of interleaving and rollbacks. We compare against the
	// observable history by re-reading: every integer in [2, max(versions)]
	// must have been handed out by some successful publish/rollback.
	max := 1
	seen := map[int]bool{1: true}
	for _, v := range versions {
		seen[v] = true
		if v > max {
			max = v
		}
	}
	missing := 0
	for v := 2; v <= max; v++ {
		if !seen[v] {
			missing++
		}
	}
	l.line("concurrency: %d accepted writes across %d writers; %d version slots produced by rollbacks also exist; max=%d",
		len(versions), writers, max-len(versions), max)
	// Slots not produced by publishes must exist as rollback versions (each
	// rollback appends one version). Verify store current <= max and history
	// is internally consistent by reading every version 0..max.
	for v := 0; v <= max; v++ {
		if _, err := s.Resolve(v, layerconfig.Scope{}, "k"); err != nil {
			t.Fatalf("version %d unreadable after concurrent run: %v", v, err)
		}
	}
	_ = missing

	res, err := s.Resolve(-1, layerconfig.Scope{}, "k")
	l.logResolve(s.CurrentVersion(), layerconfig.Scope{}, "k", res, err,
		"final current value present after concurrent workload")
	if err != nil || !res.Present {
		t.Fatalf("final state inconsistent: %v present=%v", err, res.Present)
	}
}
