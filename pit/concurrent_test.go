package pit

import (
	"sync"
	"testing"

	"ontology/segstore"
)

func TestConcurrentOperations(t *testing.T) {
	m := NewManager(50)
	base := []segstore.Doc{dd("a", 1), dd("b", 2), dd("c", 3)}
	if _, err := m.AddSegment(0, base); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			now := int64(1 + g)
			for i := 0; i < 50; i++ {
				p, err := m.Open(now, 1_000_000_000)
				if err == nil {
					_, _ = m.Search(now, p.ID, 10, nil, 0)
					_ = m.Close(now, p.ID)
				}
			}
		}(g)
	}
	wg.Wait()

	// View still contains exactly the original three live documents.
	es, err := m.Search(20, 0, 100, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(es); !eqStrings(got, []string{"a", "b", "c"}) {
		t.Fatalf("view after concurrency: %v", got)
	}
}
