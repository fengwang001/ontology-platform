package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "seed", "a", "ab", "b")

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 100; step++ {
				docID := "concurrent-" + string(rune('a'+worker%26))
				switch step % 5 {
				case 0:
					_ = e.Index(docID, []string{"a", "ab", "ape"})
				case 1:
					_ = e.Remove(docID)
				case 2:
					_ = e.Pick("a")
				case 3:
					_, _ = e.Expand("a ", 10)
				case 4:
					_, _ = e.Expand("a", 2)
				}
			}
		}(worker)
	}
	wg.Wait()

	got, err := e.Expand("a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) == 0 {
		t.Fatal("concurrent operations should retain the seed document and prefix candidates")
	}
}
