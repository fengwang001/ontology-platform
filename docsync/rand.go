package docsync

import (
	"sync/atomic"
	"time"
)

var priorityState atomic.Uint64

func init() {
	priorityState.Store(uint64(time.Now().UnixNano()) ^ 0x9E3779B97F4A7C15)
}

// nextPriority returns a pseudo-random treap priority via splitmix64.
func nextPriority() uint64 {
	for {
		old := priorityState.Load()
		z := old + 0x9E3779B97F4A7C15
		if priorityState.CompareAndSwap(old, z) {
			z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
			z = (z ^ (z >> 27)) * 0x94D049BB133111EB
			return z ^ (z >> 31)
		}
	}
}
