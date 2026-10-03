package hotrank

import (
	"sort"
)

// naiveBoard is a literal bucket-per-rule reimplementation used as an
// independent oracle in randomized differential tests.
type naiveBoard struct {
	l, w, m, k, hs int64
	hasClock       bool
	c              int64
	buckets        map[int64]map[string]int64
	prevRank       map[string]int
	hc             map[string]int64
}

func newNaive(l, w, m, k, hs int64) *naiveBoard {
	return &naiveBoard{
		l: l, w: w, m: m, k: k, hs: hs,
		buckets:  map[int64]map[string]int64{},
		prevRank: map[string]int{},
		hc:       map[string]int64{},
	}
}

type naiveItem struct {
	id     string
	score  int64
	rank   int
	change int
	new    bool
}

type naiveResult struct {
	board   []naiveItem
	dropped []DroppedItem
}

func (n *naiveBoard) scoresAt(cur int64) map[string]int64 {
	sums := map[string]int64{}
	for bn, m := range n.buckets {
		if bn > cur-n.w && bn <= cur {
			for id, v := range m {
				sums[id] += v
			}
		}
	}
	return sums
}

func (n *naiveBoard) compute(sums map[string]int64) naiveResult {
	low := n.m - n.m/4
	type cand struct {
		id    string
		score int64
	}
	var cands []cand
	for id, s := range sums {
		line := n.m
		if _, ok := n.prevRank[id]; ok && n.hc[id] < n.hs {
			line = low
		}
		if s >= line {
			cands = append(cands, cand{id, s})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].id < cands[j].id
	})
	rankOf := map[string]int{}
	for i, c := range cands {
		if i > 0 && cands[i-1].score == c.score {
			rankOf[c.id] = rankOf[cands[i-1].id]
		} else {
			rankOf[c.id] = i + 1
		}
	}
	elected := cands
	if int64(len(elected)) > n.k {
		elected = elected[:n.k]
	}
	res := naiveResult{board: []naiveItem{}, dropped: []DroppedItem{}}
	onBoard := map[string]bool{}
	for _, c := range elected {
		it := naiveItem{id: c.id, score: c.score, rank: rankOf[c.id]}
		if prev, ok := n.prevRank[c.id]; ok {
			it.change = prev - it.rank
		} else {
			it.new = true
		}
		res.board = append(res.board, it)
		onBoard[c.id] = true
	}
	for id, prev := range n.prevRank {
		if !onBoard[id] {
			res.dropped = append(res.dropped, DroppedItem{ID: id, PreviousRank: prev})
		}
	}
	sort.Slice(res.dropped, func(i, j int) bool {
		if res.dropped[i].PreviousRank != res.dropped[j].PreviousRank {
			return res.dropped[i].PreviousRank < res.dropped[j].PreviousRank
		}
		return res.dropped[i].ID < res.dropped[j].ID
	})
	return res
}

func (n *naiveBoard) add(id string, d, t int64) error {
	if id == "" || d < 1 || d > 1_000_000_000 || t < 0 || t > maxTime {
		return ErrInvalidArgument
	}
	newClock := n.c
	if !n.hasClock || t > n.c {
		newClock = t
	}
	cur := newClock / n.l
	bn := t / n.l
	if bn <= cur-n.w {
		return ErrExpired
	}
	// Post-advance window scores with the new delta applied.
	sums := n.scoresAt(cur)
	if sums[id]+d > maxScore {
		return ErrScoreOverflow
	}
	n.c = newClock
	n.hasClock = true
	m := n.buckets[bn]
	if m == nil {
		m = map[string]int64{}
		n.buckets[bn] = m
	}
	m[id] += d
	return nil
}

func (n *naiveBoard) snapshot(now int64) (naiveResult, error) {
	if now < 0 || now > maxTime {
		return naiveResult{}, ErrInvalidArgument
	}
	if n.hasClock && now < n.c {
		return naiveResult{}, ErrClockRewind
	}
	n.c = now
	n.hasClock = true
	cur := now / n.l
	sums := n.scoresAt(cur)
	for _, s := range sums {
		if s > maxScore {
			return naiveResult{}, ErrScoreOverflow
		}
	}
	res := n.compute(sums)
	newPrev := map[string]int{}
	newHc := map[string]int64{}
	for _, it := range res.board {
		newPrev[it.id] = it.rank
		if it.score >= n.m {
			newHc[it.id] = 0
		} else if old, ok := n.hc[it.id]; ok {
			newHc[it.id] = old + 1
		} else {
			newHc[it.id] = 0
		}
	}
	n.prevRank = newPrev
	n.hc = newHc
	return res, nil
}

func (n *naiveBoard) peek(now int64) (naiveResult, error) {
	if now < 0 || now > maxTime {
		return naiveResult{}, ErrInvalidArgument
	}
	if n.hasClock && now < n.c {
		return naiveResult{}, ErrClockRewind
	}
	cur := now / n.l
	sums := n.scoresAt(cur)
	for _, s := range sums {
		if s > maxScore {
			return naiveResult{}, ErrScoreOverflow
		}
	}
	return n.compute(sums), nil
}

func (n *naiveBoard) score(id string) int64 {
	cur := n.c / n.l
	return n.scoresAt(cur)[id]
}
