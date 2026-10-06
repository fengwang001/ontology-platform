package difftest

import "math/rand"

func newLocalRng(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }
