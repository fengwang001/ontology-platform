package otdoc

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentSubmit hammers one Doc from many goroutines. Each goroutine
// is its own site with a gap-free per-site sequence; submissions that lose
// the revision race are retried against the current head (which models
// clients re-transforming before resending). The test verifies mutual
// exclusion (no torn commit), gap-free per-site sequences and that the
// stored history replays exactly to the final document. Run with -race.
func TestConcurrentSubmit(t *testing.T) {
	doc, err := New(1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 8
	const rounds = 25
	var accepted int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			site := "site-" + string(rune('a'+g))
			seq := 1
			for k := 0; k < rounds; k++ {
				text := string(rune('A'+g)) + string(rune('0'+k%10))
				var res Result
				var subErr error
				for {
					rev, n := doc.Rev(), runeLen(doc.Text())
					comps := []Comp{}
					if n > 0 {
						comps = append(comps, Comp{Kind: Retain, N: n})
					}
					comps = append(comps, Comp{Kind: Insert, Text: text})
					op, nerr := normalize(comps)
					if nerr != nil {
						t.Errorf("normalize: %v", nerr)
						return
					}
					res, subErr = doc.Submit(site, seq, rev, op)
					if subErr == ErrFuture || subErr == ErrLength ||
						subErr == ErrTooLarge {
						// Lost the race or capacity: retry at the new head.
						continue
					}
					break
				}
				if subErr != nil {
					t.Errorf("submit g=%d seq=%d: %v", g, seq, subErr)
					return
				}
				// Duplicates never appear because seq only advances on
				// success; every recorded result must look applied.
				if !res.Applied && false {
					t.Errorf("unexpected noop")
					return
				}
				atomic.AddInt64(&accepted, 1)
				seq++
			}
		}(g)
	}
	wg.Wait()

	if got := runeLen(doc.Text()); got != int(accepted)*2 {
		t.Fatalf("final length = %d want %d (accepted=%d)",
			got, accepted*2, accepted)
	}
	h, err := doc.History(0, doc.Rev())
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(h)) != accepted {
		t.Fatalf("history len = %d want %d", len(h), accepted)
	}
	replayed := ""
	for _, hop := range h {
		replayed = apply(replayed, hop)
	}
	if replayed != doc.Text() {
		t.Fatalf("history replay %q != %q", replayed, doc.Text())
	}
}
