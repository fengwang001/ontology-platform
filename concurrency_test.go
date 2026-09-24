package ontology

import (
	"strconv"
	"sync"
	"testing"
)

// Locate must be safe to call concurrently with Add/Remove: no panic, no
// empty result, and every result must be a node that genuinely belongs to
// the universe of nodes in play. Run with -race.
func TestConcurrentLocateAddRemove(t *testing.T) {
	r := New()
	const permanent = "node-permanent"
	if err := r.Add(permanent, 50); err != nil {
		t.Fatalf("Add permanent: %v", err)
	}
	universe := nodeIDs(8)
	allowed := map[string]bool{permanent: true}
	for _, id := range universe {
		allowed[id] = true
	}
	stop := make(chan struct{})
	var writers, readers sync.WaitGroup
	for w := 0; w < 2; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 300; i++ {
				id := universe[(w+i)%len(universe)]
				_ = r.Add(id, 20) // ErrNodeExists is fine here
				_ = r.Remove(id)  // ErrNodeNotFound is fine here
			}
		}(w)
	}
	for g := 0; g < 8; g++ {
		readers.Add(1)
		go func(g int) {
			defer readers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := "key-" + strconv.Itoa(g) + "-" + strconv.Itoa(i)
				got, err := r.Locate(key)
				if err != nil {
					t.Errorf("Locate(%q) errored on a non-empty ring: %v", key, err)
					return
				}
				if got == "" {
					t.Errorf("Locate(%q) returned empty node", key)
					return
				}
				if !allowed[got] {
					t.Errorf("Locate(%q) returned %q, not a node of this ring", key, got)
					return
				}
			}
		}(g)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
}
