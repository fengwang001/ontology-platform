package ontology

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

func TestConcurrentUpsertsDistinctIDs(t *testing.T) {
	s := New("color")
	const workers = 8
	const perWorker = 250
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("w%02d-%04d", w, i)
				s.Upsert(id, map[string]any{"color": "red"})
			}
		}(w)
	}
	wg.Wait()
	if s.Len() != workers*perWorker {
		t.Fatalf("Len=%d, want %d (lost rows)", s.Len(), workers*perWorker)
	}
	got := s.Query("color", "red")
	if len(got) != workers*perWorker {
		t.Fatalf("Query returned %d IDs, want %d", len(got), workers*perWorker)
	}
	seen := make(map[string]bool, len(got))
	for _, id := range got {
		if seen[id] {
			t.Fatalf("ID %s appears twice", id)
		}
		seen[id] = true
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentUpsertsSameID(t *testing.T) {
	s := New("color")
	const ids = 50
	const workers = 16
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ids; i++ {
				id := fmt.Sprintf("e%02d", i)
				s.Upsert(id, map[string]any{"color": fmt.Sprintf("c%d", w)})
			}
		}(w)
	}
	wg.Wait()
	if s.Len() != ids {
		t.Fatalf("Len=%d, want %d (duplicate upserts inflated count)", s.Len(), ids)
	}
	if s.TotalEntries() != ids {
		t.Fatalf("TotalEntries=%d, want %d", s.TotalEntries(), ids)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentReadWriteAtomicity(t *testing.T) {
	s := New("color")
	const rows = 200
	for i := 0; i < rows; i++ {
		s.Upsert(fmt.Sprintf("e%03d", i), map[string]any{"color": "red"})
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var writers sync.WaitGroup
	errCh := make(chan error, 16)

	// Readers: Verify must never observe a half-updated index, and every
	// query result must be sorted and duplicate-free.
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.Verify(); err != nil {
					errCh <- err
					return
				}
				got := s.Query("color", "red")
				if !sort.StringsAreSorted(got) {
					errCh <- fmt.Errorf("query result not sorted")
					return
				}
				prev := ""
				for i, id := range got {
					if i > 0 && id == prev {
						errCh <- fmt.Errorf("duplicate ID %s in query result", id)
						return
					}
					prev = id
				}
			}
		}()
	}

	// Writers: flip every row between red and blue, and rewrite rows.
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for round := 0; round < 20; round++ {
				for i := w; i < rows; i += 4 {
					id := fmt.Sprintf("e%03d", i)
					color := "red"
					if (i+round)%2 == 0 {
						color = "blue"
					}
					s.Upsert(id, map[string]any{"color": color})
				}
			}
		}(w)
	}

	writers.Wait()
	close(stop)
	readers.Wait()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	default:
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}
