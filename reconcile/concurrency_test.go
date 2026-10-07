package reconcile

import (
	"encoding/json"
	"sync"
	"testing"
)

// Concurrent calls on the same input set must all produce byte-identical
// results, and the shared inputs must remain unmodified. Run with -race.
func TestConcurrentReconcileIdenticalResults(t *testing.T) {
	raws := [][]byte{
		snap(t, "r1", 2, 6, map[string]map[string]VersionedValue{
			"o1": {"a": vv("x", 3), "b": vv("p", 6)},
			"o2": {"a": vv("keep", 1)},
		}),
		snap(t, "r2", 1, 9, map[string]map[string]VersionedValue{
			"o1": {"a": vv("y", 4), "b": vv("q", 8)},
			"o3": {"z": vv("late", 9)},
		}),
		snap(t, "r3", 1, 6, map[string]map[string]VersionedValue{
			"o1": {"a": vv("z", 4)}, // ties with r2 on priority for o1.a
		}),
		[]byte(`{"replica":{"id":"r4","priority":0},"position":`), // corrupted
	}
	frozen := make([][]byte, len(raws))
	for i, r := range raws {
		frozen[i] = append([]byte(nil), r...)
	}

	r := New(Options{})
	want, err := r.Reconcile(raws)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const workers = 16
	const iterations = 50
	var wg sync.WaitGroup
	errs := make(chan string, workers*iterations)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				got, err := r.Reconcile(raws)
				if err != nil {
					errs <- err.Error()
					return
				}
				gotJSON, _ := json.Marshal(got)
				if string(gotJSON) != string(wantJSON) {
					errs <- "concurrent result diverged"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent run: %s", e)
	}

	for i := range raws {
		if string(raws[i]) != string(frozen[i]) {
			t.Fatalf("input %d mutated by reconciliation", i)
		}
	}
}
