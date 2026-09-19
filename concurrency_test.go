package ontology

import (
	"sync"
	"testing"
)

// Concurrent Adds must not lose rows or undercount any group, and
// concurrent Snapshots must never observe a half-updated group.
// Run with -race to also catch data races.
func TestConcurrentAddAndSnapshot(t *testing.T) {
	const (
		workers       = 8
		rowsPerWorker = 2000
		groups        = 4
	)
	agg := NewAggregator([]string{"g"}, "v")
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := string(rune('a' + w%groups))
			for i := 0; i < rowsPerWorker; i++ {
				agg.Add(map[string]any{"g": name, "v": int64(1)})
			}
		}(w)
	}

	// Snapshot concurrently while Adds are in flight.
	stop := make(chan struct{})
	var snapWG sync.WaitGroup
	snapWG.Add(1)
	go func() {
		defer snapWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			results, err := agg.Snapshot()
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			var total int64
			for _, r := range results {
				if r.Count < 0 || r.Skipped != 0 {
					t.Errorf("inconsistent group: %+v", r)
					return
				}
				// Every summed row contributed exactly 1: Sum must
				// equal Count, never a half-updated state.
				if r.IntSum != r.Count {
					t.Errorf("half update: Count=%d IntSum=%d",
						r.Count, r.IntSum)
					return
				}
				total += r.Count
			}
			if total > workers*rowsPerWorker {
				t.Errorf("total %d exceeds fed rows", total)
				return
			}
		}
	}()

	wg.Wait()
	close(stop)
	snapWG.Wait()

	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var total int64
	perGroup := map[string]int64{}
	for _, r := range results {
		total += r.Count
		perGroup[KeyString(r.Key)] = r.Count
		if r.IntSum != r.Count {
			t.Fatalf("group %v: IntSum %d != Count %d",
				r.Key, r.IntSum, r.Count)
		}
	}
	if total != workers*rowsPerWorker {
		t.Fatalf("lost rows: total %d, want %d",
			total, workers*rowsPerWorker)
	}
	wantPerGroup := int64(workers / groups * rowsPerWorker)
	for name, count := range perGroup {
		if count != wantPerGroup {
			t.Fatalf("group %s: count %d, want %d",
				name, count, wantPerGroup)
		}
	}
}
