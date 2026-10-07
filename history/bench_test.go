package history_test

import (
	"fmt"
	"testing"
	"time"

	"ontology/history"
)

// BenchmarkTraverseSameDocument shows that locating the traversal target
// and deciding same-document does not depend on history length: ns/op
// stays flat as the entry list grows from 1e3 to 1e6.
//
//	go test ./history/ -bench=TraverseSameDocument -benchtime=100x
func BenchmarkTraverseSameDocument(b *testing.B) {
	for _, n := range []int{1_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			k, err := history.NewKernel(history.Config{
				CacheCapacity: 4, CacheTTL: time.Hour, Now: t0,
			})
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if _, err := k.Navigate(fmt.Sprintf("u%d", i), nil, i > 0, at(i)); err != nil {
					b.Fatal(err)
				}
			}
			steady := at(n + 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				k.Traverse(-1, steady)
				k.Drain()
				k.Traverse(1, steady)
				k.Drain()
			}
		})
	}
}

// BenchmarkCacheEvict shows that picking the eviction victim does not
// grow linearly with the number of cached documents (heap, O(log n)).
//
//	go test ./history/ -bench=CacheEvict -benchtime=100x
func BenchmarkCacheEvict(b *testing.B) {
	for _, capacity := range []int{100, 10_000, 100_000} {
		b.Run(fmt.Sprintf("cached=%d", capacity), func(b *testing.B) {
			k, err := history.NewKernel(history.Config{
				CacheCapacity: capacity, CacheTTL: time.Hour, Now: t0,
			})
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i <= capacity; i++ {
				if _, err := k.Navigate(fmt.Sprintf("u%d", i), nil, false, at(i)); err != nil {
					b.Fatal(err)
				}
			}
			steady := at(capacity + 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Cross-document navigation inserts the current document
				// into a full cache, forcing one eviction each time.
				if _, err := k.Navigate(fmt.Sprintf("x%d", i), nil, false, steady); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
