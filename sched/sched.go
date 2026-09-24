// Package sched turns an accepted dep.Graph into bounded-parallelism rounds
// and replays them with real concurrency inside each round. It depends on dep.
package sched

import (
	"sync"

	"ontology/dep"
)

// Schedule is a round assignment: Rounds[r] holds the seqs scheduled in round
// r+1 (scan order), RoundOf[seq-1] is that seq's 1-based round. MaxPar is the
// widest round of this schedule.
type Schedule struct {
	P       int
	Rounds  [][]int64
	RoundOf []int
	Total   int
	MaxPar  int
}

// Build schedules g under parallelism cap p. p <= 0 means unlimited. In each
// round seqs are scanned ascending and a txn is taken only when every
// dependency finished in a strictly earlier round; a round stops at p.
func Build(g *dep.Graph, p int) *Schedule {
	n := g.Len()
	capP := p
	if p <= 0 {
		capP = n
	}
	placed, roundOf := make([]bool, n), make([]int, n)
	var rounds [][]int64
	for done, round := 0, 1; done < n; round++ {
		cur := []int64{}
		for seq := 1; seq <= n && len(cur) < capP; seq++ {
			if placed[seq-1] {
				continue
			}
			_, deps, _, _ := g.Record(int64(seq))
			ready := true
			for _, d := range deps {
				if r := roundOf[d-1]; r == 0 || r >= round {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			cur, placed[seq-1], roundOf[seq-1] = append(cur, int64(seq)), true, round
			done++
		}
		rounds = append(rounds, cur)
	}
	maxPar := 0
	for _, r := range rounds {
		if len(r) > maxPar {
			maxPar = len(r)
		}
	}
	return &Schedule{P: p, Rounds: rounds, RoundOf: roundOf, Total: len(rounds), MaxPar: maxPar}
}

// MaxParallel returns the widest round when no parallelism cap is imposed.
func MaxParallel(g *dep.Graph) int {
	return Build(g, 0).MaxPar
}

// Replay executes rounds concurrently: within a round all txn goroutines are
// released together and apply value=seq to their write keys; a barrier joins
// the round before the next one starts, matching commit-order visibility.
func (s *Schedule) Replay(g *dep.Graph) map[string]int64 {
	state := map[string]int64{}
	var mu sync.Mutex
	for _, round := range s.Rounds {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, seq := range round {
			wg.Add(1)
			go func(seq int64) {
				defer wg.Done()
				<-start
				w, _, _, _ := g.Record(seq)
				mu.Lock()
				for _, k := range w {
					state[k] = seq
				}
				mu.Unlock()
			}(seq)
		}
		close(start)
		wg.Wait()
	}
	return state
}
