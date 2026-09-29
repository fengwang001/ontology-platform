package chainreplication

import (
	"math/rand"
	"sync"
)

var (
	randMu sync.Mutex
	rng    = rand.New(rand.NewSource(1))
)

// SeedRandom makes scheduling-based tests reproducible.
func SeedRandom(seed int64) {
	randMu.Lock()
	defer randMu.Unlock()
	rng = rand.New(rand.NewSource(seed))
}

func randFloat() float64 {
	randMu.Lock()
	defer randMu.Unlock()
	return rng.Float64()
}

func randInt63n(n int64) int64 {
	randMu.Lock()
	defer randMu.Unlock()
	if n <= 0 {
		return 0
	}
	return rng.Int63n(n)
}
