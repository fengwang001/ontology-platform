package dedup

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

const (
	concWorkers = 8
	concKeys    = 64
	concRounds  = 2000
)

func feedConcurrent(d *Deduper) {
	var wg sync.WaitGroup
	for w := 0; w < concWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < concRounds; r++ {
				for k := 0; k < concKeys; k++ {
					d.Add(map[string]any{
						"id":      int64(k),
						"payload": fmt.Sprintf("w%d-r%d", w, r),
					})
				}
			}
		}(w)
	}
	wg.Wait()
}

func feedSerial(d *Deduper) {
	for w := 0; w < concWorkers; w++ {
		for r := 0; r < concRounds; r++ {
			for k := 0; k < concKeys; k++ {
				d.Add(map[string]any{
					"id":      int64(k),
					"payload": fmt.Sprintf("w%d-r%d", w, r),
				})
			}
		}
	}
}

func keySet(rows []map[string]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprint(r["id"])
	}
	sort.Strings(out)
	return out
}

// TestConcurrentAddMatchesSerial: after concurrent Adds the group count
// and the set of dedup keys must equal the serial run's, and no group
// may appear twice.
func TestConcurrentAddMatchesSerial(t *testing.T) {
	for _, mode := range []Mode{KeepFirst, KeepLast} {
		conc := New([]string{"id"}, mode)
		feedConcurrent(conc)
		ser := New([]string{"id"}, mode)
		feedSerial(ser)
		wantProcessed := int64(concWorkers * concRounds * concKeys)
		if got := conc.Processed(); got != wantProcessed {
			t.Fatalf("mode=%d: Processed = %d, want %d (lost rows?)",
				mode, got, wantProcessed)
		}
		if conc.Processed() != ser.Processed() {
			t.Fatalf("mode=%d: processed mismatch conc=%d ser=%d",
				mode, conc.Processed(), ser.Processed())
		}
		if conc.Groups() != ser.Groups() {
			t.Fatalf("mode=%d: groups conc=%d ser=%d", mode, conc.Groups(), ser.Groups())
		}
		ck, sk := keySet(conc.Snapshot()), keySet(ser.Snapshot())
		if fmt.Sprint(ck) != fmt.Sprint(sk) {
			t.Fatalf("mode=%d: key sets differ\nconc=%v\nser=%v", mode, ck, sk)
		}
		if len(ck) != concKeys {
			t.Fatalf("mode=%d: got %d groups, want %d", mode, len(ck), concKeys)
		}
	}
}

// TestConcurrentSnapshotConsistent runs Snapshots while Adds are in
// flight; every snapshot must be a consistent, fully sorted view.
func TestConcurrentSnapshotConsistent(t *testing.T) {
	d := New([]string{"id"}, KeepLast)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < concWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
				}
				d.Add(map[string]any{"id": int64(i % concKeys), "payload": w})
			}
		}(w)
	}
	for i := 0; i < 200; i++ {
		snap := d.Snapshot()
		if len(snap) > concKeys {
			t.Fatalf("snapshot has %d rows, more than %d keys", len(snap), concKeys)
		}
		seen := make(map[int64]bool, len(snap))
		prev := int64(-1)
		for j, row := range snap {
			id := row["id"].(int64)
			if seen[id] {
				t.Fatalf("snapshot %d: group %d appears twice", i, id)
			}
			seen[id] = true
			if j > 0 && id < prev {
				t.Fatalf("snapshot %d: not sorted at row %d", i, j)
			}
			prev = id
		}
	}
	close(done)
	wg.Wait()
	if d.Groups() != concKeys {
		t.Fatalf("final groups = %d, want %d", d.Groups(), concKeys)
	}
}
