package rng

import (
	"math/rand/v2"
)

type Source struct {
	state *rand.Rand
}

func New(seed uint64) Source {
	return Source{state: rand.New(rand.NewPCG(seed, seed))}
}

func (r Source) Intn(n int) int {
	if n <= 0 {
		panic("rng: non-positive interval for Intn")
	}
	return r.state.IntN(n)
}
