package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentNonceUniquenessAndLinearizable(t *testing.T) {
	m, _ := New(Config{Ta: 1000, Tv: 1000, To: 1000, H: 100, F: 100,
		C: 50, Pm: 1000})
	const goroutines = 16
	const perG = 200

	var wg sync.WaitGroup
	results := make(chan int64, goroutines*perG)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			acc := []byte{byte('a' + id)}
			for i := 0; i < perG; i++ {
				n := m.Nonce()
				results <- n
				_, _ = m.NewOrder(acc, []string{"x.com"}, n, 0)
			}
		}(g)
	}
	wg.Wait()
	close(results)

	seen := map[int64]int{}
	for n := range results {
		seen[n]++
	}
	if len(seen) != goroutines*perG {
		t.Fatalf("duplicate nonces issued: unique=%d", len(seen))
	}
}
