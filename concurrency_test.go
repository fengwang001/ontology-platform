package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentSameCursorIdempotent(t *testing.T) {
	s, _ := seed(t, 50)
	base, err := s.Scan("", 10)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	const workers = 16
	pages := make([]Page, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pages[i], errs[i] = s.Scan(base.Next, 7)
		}(i)
	}
	wg.Wait()
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if !equalKeys(pageKeys(pages[i]), pageKeys(pages[0])) {
			t.Fatalf("worker %d page differs: %v vs %v",
				i, pageKeys(pages[i]), pageKeys(pages[0]))
		}
		if pages[i].Next != pages[0].Next {
			t.Fatalf("worker %d next cursor differs", i)
		}
		if pages[i].HasMore != pages[0].HasMore || pages[i].Dropped != pages[0].Dropped {
			t.Fatalf("worker %d flags differ", i)
		}
	}
}

func TestConcurrentScanAndWrite(t *testing.T) {
	s, _ := seed(t, 100)
	stop := make(chan struct{})

	var writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("w%d-%04d", w, i%200)
				if i%2 == 0 {
					s.Put(key, i)
				} else {
					s.Delete(key)
				}
			}
		}(w)
	}

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			cursor := ""
			for i := 0; i < 30; i++ {
				p, err := s.Scan(cursor, 9)
				if err != nil {
					t.Errorf("Scan: %v", err)
					return
				}
				seen := make(map[string]bool)
				for _, it := range p.Items {
					if seen[it.Key] {
						t.Errorf("duplicate key in page: %s", it.Key)
					}
					seen[it.Key] = true
				}
				cursor = p.Next
				if !p.HasMore {
					cursor = ""
				}
			}
		}()
	}

	readers.Wait()
	close(stop)
	writers.Wait()
}

func TestConcurrentNewSessions(t *testing.T) {
	s, want := seed(t, 20)
	const workers = 8
	results := make([][]string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cursor := ""
			for {
				p, err := s.Scan(cursor, 6)
				if err != nil {
					t.Errorf("Scan: %v", err)
					return
				}
				results[i] = append(results[i], pageKeys(p)...)
				cursor = p.Next
				if !p.HasMore {
					return
				}
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < workers; i++ {
		if !equalKeys(results[i], want) {
			t.Fatalf("worker %d traversed %v, want %v", i, results[i], want)
		}
	}
}
