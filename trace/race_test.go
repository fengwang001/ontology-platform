package trace_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/trace"
)

// TestConcurrentAccess hammers one span with mixed readers, writers
// and derivations. Run with -race; it must stay clean.
func TestConcurrentAccess(t *testing.T) {
	n := 0
	var genMu sync.Mutex
	gen := func() trace.SpanID {
		genMu.Lock()
		defer genMu.Unlock()
		n++
		return trace.SpanID(fmt.Sprintf("id%06d", n))
	}

	root := trace.NewRoot(gen, true)
	root.SetBaggage("base", "visible-at-derivation")

	const workers = 8
	const childrenPerWorker = 50

	var wg sync.WaitGroup
	ids := make(chan trace.SpanID, workers*childrenPerWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", w)
			for i := 0; i < childrenPerWorker; i++ {
				root.SetBaggage(key, fmt.Sprintf("v%d", i))
				root.Baggage("base")
				root.BaggageKeys()
				root.Marshal()
				child := root.Child()
				// Baggage present at derivation must be visible.
				if v, ok := child.Baggage("base"); !ok || v != "visible-at-derivation" {
					t.Errorf("child missing derivation-time baggage: %q,%v", v, ok)
				}
				if child.TraceID() != root.TraceID() {
					t.Errorf("child trace id %q != %q", child.TraceID(), root.TraceID())
				}
				ids <- child.SpanID()
			}
		}(w)
	}
	wg.Wait()
	close(ids)

	seen := make(map[trace.SpanID]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate span id under concurrency: %q", id)
		}
		seen[id] = true
	}
	if len(seen) != workers*childrenPerWorker {
		t.Fatalf("got %d unique ids, want %d", len(seen), workers*childrenPerWorker)
	}
}

// TestConcurrentParse exercises Parse alongside Marshal on the same span.
func TestConcurrentParse(t *testing.T) {
	root := trace.NewRoot(seqGen(), true)
	root.SetBaggage("a", "1")
	wire := root.Marshal()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s, err := trace.Parse(wire)
				if err != nil {
					t.Errorf("Parse: %v", err)
					return
				}
				s.SetBaggage("b", "2")
				s.Marshal()
				root.SetBaggage("c", "3")
				root.Marshal()
			}
		}()
	}
	wg.Wait()
}
