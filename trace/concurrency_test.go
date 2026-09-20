package trace

import (
	"fmt"
	"sync"
	"testing"
)

// Semantics 7: concurrent SetBaggage / Baggage / Child / Marshal on
// the same span is race-free (run with -race); concurrently derived
// children all get distinct span IDs and see the baggage present at
// their derivation time.
func TestConcurrentAccess(t *testing.T) {
	// The generator itself is not thread-safe; Span must serialize
	// calls to it.
	n := 0
	gen := func() SpanID {
		n++
		return SpanID(fmt.Sprintf("id%d", n))
	}
	root := NewRoot(gen, true)
	root.SetBaggage("boot", "yes")
	const workers = 32
	var wg sync.WaitGroup
	ids := make(chan SpanID, workers*64)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", w%8)
			for i := 0; i < 50; i++ {
				root.SetBaggage(key, fmt.Sprintf("v%d", i))
				root.Baggage(key)
				root.BaggageKeys()
				_ = root.Marshal()
				c := root.Child()
				ids <- c.SpanID()
				// Every child sees the baggage that predates the
				// concurrent phase, regardless of timing.
				if v, ok := c.Baggage("boot"); !ok || v != "yes" {
					t.Errorf("child missing pre-existing baggage: %q,%v", v, ok)
				}
			}
		}(w)
	}
	wg.Wait()
	close(ids)
	seen := make(map[SpanID]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate child SpanID %q", id)
		}
		seen[id] = true
	}
	if len(seen) != workers*50 {
		t.Fatalf("got %d distinct child IDs, want %d", len(seen), workers*50)
	}
}

// Semantics 7 (cont.): children derived concurrently each carry an
// independent baggage map; hammering one child's baggage never
// disturbs another child's snapshot.
func TestConcurrentChildrenIndependent(t *testing.T) {
	root := NewRoot(nil, false)
	root.SetBaggage("shared", "base")
	const kids = 16
	children := make([]*Span, kids)
	var wg sync.WaitGroup
	for i := range children {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			children[i] = root.Child()
		}(i)
	}
	wg.Wait()
	for i, c := range children {
		wg.Add(1)
		go func(i int, c *Span) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.SetBaggage("shared", fmt.Sprintf("kid%d", i))
				c.Marshal()
			}
		}(i, c)
	}
	wg.Wait()
	for i, c := range children {
		if v, _ := c.Baggage("shared"); v != fmt.Sprintf("kid%d", i) {
			t.Fatalf("child %d: shared = %q, want kid%d", i, v, i)
		}
	}
	if v, _ := root.Baggage("shared"); v != "base" {
		t.Fatalf("root: shared = %q, want base", v)
	}
}
