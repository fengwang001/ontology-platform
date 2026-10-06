package fedalloc

import (
	"math/big"
	"sort"
)

// Independent reference allocator for differential testing. It follows the
// specification round by round but is written from scratch (no shared
// helpers) and keeps a running explicit "allocated this round" tally, which
// makes it structurally different from the production bulk implementation.

type naiveCluster struct {
	name            string
	weight          int64
	minReplicas     int64
	cap             int64
	available       bool
	currentReplicas int64
}

func naiveAllocate(cs []naiveCluster, total int64) (map[string]int64, ErrorKind, bool) {
	target := map[string]int64{}
	sumMin := int64(0)
	for _, c := range cs {
		if !c.available {
			target[c.name] = 0
			continue
		}
		if c.minReplicas > c.cap {
			return nil, KindConfigConflict, false
		}
		target[c.name] = c.minReplicas
		sumMin += c.minReplicas
	}
	if sumMin > total {
		return nil, KindMinExceedsTotal, false
	}
	remaining := total - sumMin

	byName := map[string]naiveCluster{}
	for _, c := range cs {
		byName[c.name] = c
	}

	for remaining > 0 {
		// Determine survivors and the round weight sum.
		var live []string
		ws := int64(0)
		for _, c := range cs {
			if c.available && c.weight > 0 && target[c.name] < c.cap {
				live = append(live, c.name)
				ws += c.weight
			}
		}
		if len(live) == 0 {
			return nil, KindInsufficientCapacity, false
		}

		type info struct {
			name     string
			give     int64 // units actually granted so far this round
			remN     *big.Int
			pinned   bool
			headroom int64
		}
		infos := map[string]*info{}
		var ordered []*info

		// Pass 1: proportional floors with pinning at the cap.
		for _, n := range live {
			c := byName[n]
			headroom := c.cap - target[n]
			num := new(big.Int).Mul(big.NewInt(remaining), big.NewInt(c.weight))
			q, rem := new(big.Int).QuoRem(num, big.NewInt(ws), new(big.Int))
			f := q.Int64()
			inf := &info{name: n, remN: rem, headroom: headroom}
			if f >= headroom {
				inf.give = headroom
				inf.pinned = true
			} else {
				inf.give = f
			}
			infos[n] = inf
			ordered = append(ordered, inf)
		}

		var granted int64
		for _, inf := range ordered {
			granted += inf.give
		}
		left := remaining - granted

		// Pass 2: largest-remainder +1 units. Static fractional ordering over
		// all survivors; pinned clusters cannot receive a bonus. The moment a
		// bonus fills a cluster's cap the round ends and the leftover moves to
		// a new round over the survivors.
		sort.Slice(ordered, func(i, j int) bool {
			if c := ordered[i].remN.Cmp(ordered[j].remN); c != 0 {
				return c > 0
			}
			li := byName[ordered[i].name].currentReplicas
			lj := byName[ordered[j].name].currentReplicas
			if li != lj {
				return li > lj
			}
			return ordered[i].name < ordered[j].name
		})

		roundAborted := false
		for _, inf := range ordered {
			if left == 0 {
				break
			}
			if inf.pinned || inf.give >= inf.headroom {
				continue
			}
			inf.give++
			left--
			if inf.give == inf.headroom {
				roundAborted = true
				break
			}
		}

		for _, inf := range ordered {
			target[inf.name] += inf.give
		}
		remaining = left
		_ = roundAborted
	}

	return target, 0, true
}
