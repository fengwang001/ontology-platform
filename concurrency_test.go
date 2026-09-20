package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

var (
	errOrder = errors.New("concurrent result order differs from baseline")
	errCount = errors.New("comparison count leaked across concurrent calls")
)

// 同一个 Sorter 被多协程并发调用：结果一致，各自的比较计数互不串台。
func TestConcurrentSortsIsolated(t *testing.T) {
	rows := []map[string]any{
		{"a": int64(5), "b": "x"},
		{"a": int64(1), "b": "y"},
		{"a": int64(9), "b": "z"},
		{"a": int64(3), "b": "w"},
		{"a": int64(7), "b": "v"},
	}
	s := NewSorter(SortKey{Field: "a"}, SortKey{Field: "b"})
	base, err := s.Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 20; round++ {
				res, err := s.Sort(rows)
				if err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(res.Indices(), base.Indices()) {
					errs <- errOrder
					return
				}
				if res.Stats.Comparisons != base.Stats.Comparisons {
					errs <- errCount
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
