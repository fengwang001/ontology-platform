package query

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/ival"
)

// TestConcurrentReaders 多 goroutine 同一批查询逐位一致；不依赖 sleep。
func TestConcurrentReaders(t *testing.T) {
	e, _ := buildDisjoint(t, 5000)
	qs := []ival.Interval{
		iv(0, 20), iv(1000, 1040), iv(9000, 10000), iv(20000, 20000),
	}
	ref, err := e.BatchOverlap(qs)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed)))
			for k := 0; k < 200; k++ {
				q := qs[rng.Intn(len(qs))]
				got, err := e.Overlap(q)
				if err != nil {
					errs <- err
					return
				}
				idx := 0
				for i := range qs {
					if qs[i] == q {
						idx = i
					}
				}
				if !reflect.DeepEqual(got, ref[idx]) {
					errs <- fmt.Errorf("concurrent mismatch: %v vs %v", got, ref[idx])
					return
				}
			}
		}(w + 1)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
