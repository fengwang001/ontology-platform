package scan

import (
	"sync"
	"testing"

	"ontology/zone"
)

// TestConcurrentScans: many goroutines scan the same segment with
// independent cursors; each result must equal the serial result.
func TestConcurrentScans(t *testing.T) {
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	for i := 0; i < 50; i++ {
		gr := g()
		for j := 0; j < 40; j++ {
			gr.vals = append(gr.vals, int64((i*7+j)%23))
			gr.nulls = append(gr.nulls, (i+j)%11 == 0)
		}
		groups = append(groups, gr)
	}
	seg := buildSeg(t, groups)
	preds := []zone.Pred{zone.GreaterEqual(5), zone.LessEqual(15)}
	want := NewScanner(seg, preds).All()

	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(batch int) {
			defer wg.Done()
			sc := NewScanner(seg, preds)
			var got []Row
			for {
				rows, eof := sc.Next(batch)
				got = append(got, rows...)
				if eof {
					break
				}
			}
			if len(got) != len(want) {
				errs <- "row count mismatch"
				return
			}
			for i := range want {
				if got[i] != want[i] {
					errs <- "row mismatch"
					return
				}
			}
		}(w + 1)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
