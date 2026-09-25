package shuffle

import (
	"sync/atomic"

	"ontology/rng"
)

var randomCalls atomic.Uint64

func Shuffle[T any](arr []T, seed uint64) {
	random := rng.New(seed)
	for i := len(arr) - 1; i > 0; i-- {
		j := random.Intn(i + 1)
		randomCalls.Add(1)
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func RandomCalls() uint64 {
	return randomCalls.Load()
}
