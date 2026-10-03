// Package hotrank implements a real-time hotness leaderboard over a sliding
// window of fixed-length time buckets with out-of-order events, rank-keeping
// hysteresis and rank-change comparison against the previous published board.
package hotrank

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

// Rejection reasons. Only the first applicable reason is reported, in the
// order: ErrInvalidArgument, ErrExpired, ErrClockRewind, ErrScoreOverflow.
var (
	ErrInvalidArgument = errors.New("hotrank: invalid argument")
	ErrExpired         = errors.New("hotrank: event expired")
	ErrClockRewind     = errors.New("hotrank: snapshot time before current clock")
	ErrScoreOverflow   = errors.New("hotrank: window score overflows")
)

const (
	maxTime  = int64(1_000_000_000_000_000)
	maxScore = int64(1_000_000_000_000_000)
)

// RankItem is one entry of a published leaderboard snapshot.
type RankItem struct {
	ID     string
	Score  int64
	Rank   int
	Change int // previous rank - current rank; positive means moved up.
	New    bool
}

// DroppedItem records an entry present on the previous board but absent now.
type DroppedItem struct {
	ID           string
	PreviousRank int
}

// Result is the output of a Snapshot/Peek computation.
type Result struct {
	Board   []RankItem
	Dropped []DroppedItem
}

// Board keeps sliding-window bucket scores and the last published board.
type Board struct {
	mu sync.Mutex

	L, W, M, K, Hs int64

	// hasClock is false before the first accepted Add/Snapshot.
	hasClock bool
	c        int64

	// buckets maps bucket number -> id -> delta recorded in that bucket.
	buckets map[int64]map[string]int64
	// bucketOrder holds every bucket number present in buckets.
	bucketOrder *int64Heap
	// scores is each id's sum over buckets in (cur-W, cur].
	scores map[string]int64

	// prevRank maps board id -> rank from the last published snapshot.
	prevRank map[string]int
	// hc maps board id -> consecutive published snapshots spent below M.
	hc map[string]int64

	// addExpireScans counts (bucket,id) pairs examined while expiring
	// buckets from Add. Independent of W; see tests.
	addExpireScans int64
}

// New constructs a Board.
func New(L, W, M, K, Hs int64) (*Board, error) {
	if L < 1 || L > 1_000_000_000 ||
		W < 1 || W > 1000 ||
		M < 1 || M > 1_000_000_000_000 ||
		K < 1 || K > 100 ||
		Hs < 1 || Hs > 100 {
		return nil, ErrInvalidArgument
	}
	h := &int64Heap{}
	heap.Init(h)
	return &Board{
		L:           L,
		W:           W,
		M:           M,
		K:           K,
		Hs:          Hs,
		buckets:     map[int64]map[string]int64{},
		bucketOrder: h,
		scores:      map[string]int64{},
		prevRank:    map[string]int{},
		hc:          map[string]int64{},
	}, nil
}

// Add applies one scoring event.
func (b *Board) Add(id string, d, t int64) error {
	if id == "" || d < 1 || d > 1_000_000_000 || t < 0 || t > maxTime {
		return ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	prevClock := b.c
	hadClock := b.hasClock
	if !hadClock || t > b.c {
		b.c = t
		b.hasClock = true
	}
	cur := b.c / b.L

	// Expire buckets that leave the window once the clock has advanced.
	var expired []int64
	if hadClock {
		expired = b.collectExpired(cur - b.W)
	}

	// Expiration of the event itself uses cur after the potential advance.
	bn := t / b.L
	if hadClock && bn <= cur-b.W {
		b.restoreExpired(expired)
		b.c = prevClock
		b.hasClock = hadClock
		return ErrExpired
	}

	// Tentatively apply the delta to check overflow on the post-advance window.
	tentative := b.scores[id] + d
	if tentative > maxScore {
		b.restoreExpired(expired)
		b.c = prevClock
		b.hasClock = hadClock
		return ErrScoreOverflow
	}

	// Commit.
	b.commitExpired(expired)

	m := b.buckets[bn]
	if m == nil {
		m = map[string]int64{}
		b.buckets[bn] = m
		heap.Push(b.bucketOrder, bn)
	}
	m[id] += d
	if cur, ok := b.scores[id]; ok {
		b.scores[id] = cur + d
	} else {
		b.scores[id] = d
	}
	return nil
}

// Score returns the current window score of id without changing state.
func (b *Board) Score(id string) int64 {
	if id == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.scores[id]
}

// Snapshot advances the clock and publishes a new board.
func (b *Board) Snapshot(now int64) (Result, error) {
	if now < 0 || now > maxTime {
		return Result{}, ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.hasClock && now < b.c {
		return Result{}, ErrClockRewind
	}

	prevClock := b.c
	hadClock := b.hasClock
	b.c = now
	b.hasClock = true

	newCur := now / b.L
	expired := b.collectExpired(newCur - b.W)

	// Defensive overflow check over post-advance window scores.
	for _, s := range b.scores {
		if s > maxScore {
			b.restoreExpired(expired)
			b.c = prevClock
			b.hasClock = hadClock
			return Result{}, ErrScoreOverflow
		}
	}

	b.commitExpired(expired)
	res := b.computeLocked()
	b.publishLocked(res)
	return res, nil
}

// Peek computes a snapshot without advancing the clock or publishing.
func (b *Board) Peek(now int64) (Result, error) {
	if now < 0 || now > maxTime {
		return Result{}, ErrInvalidArgument
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.hasClock && now < b.c {
		return Result{}, ErrClockRewind
	}

	// Build the hypothetical post-advance state without mutating anything.
	peekCur := now / b.L
	limit := peekCur - b.W

	sums := make(map[string]int64, len(b.scores))
	for bn, m := range b.buckets {
		if bn <= limit {
			continue
		}
		for id, v := range m {
			sums[id] += v
		}
	}
	for id, s := range sums {
		if s > maxScore {
			return Result{}, ErrScoreOverflow
		}
		_ = id
	}
	return b.computeWithScores(sums), nil
}

// hysteresisLine is M-floor(M/4) for previous-board entries with hc < Hs.
func (b *Board) hysteresisLine() int64 {
	return b.M - b.M/4
}

type cand struct {
	id    string
	score int64
}

// computeLocked builds a Result from b.scores under the current clock.
func (b *Board) computeLocked() Result {
	return b.computeWithScores(b.scores)
}

// computeWithScores builds a Result from the supplied window-score map.
func (b *Board) computeWithScores(sums map[string]int64) Result {
	lowLine := b.hysteresisLine()

	var cands []cand
	for id, s := range sums {
		line := b.M
		if _, onPrev := b.prevRank[id]; onPrev && b.hc[id] < b.Hs {
			line = lowLine
		}
		if s >= line {
			cands = append(cands, cand{id: id, score: s})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].id < cands[j].id
	})

	// Ranks among ALL candidates (ties beyond the K cut do not occupy ranks).
	rankOf := make(map[string]int, len(cands))
	for i, c := range cands {
		if i > 0 && cands[i-1].score == c.score {
			rankOf[c.id] = rankOf[cands[i-1].id]
		} else {
			rankOf[c.id] = i + 1
		}
	}

	elected := cands
	if int64(len(elected)) > b.K {
		elected = elected[:b.K]
	}

	res := Result{Board: []RankItem{}, Dropped: []DroppedItem{}}
	electedSet := map[string]bool{}
	for _, c := range elected {
		rank := rankOf[c.id]
		item := RankItem{ID: c.id, Score: c.score, Rank: rank}
		if prev, ok := b.prevRank[c.id]; ok {
			item.Change = prev - rank
		} else {
			item.New = true
		}
		res.Board = append(res.Board, item)
		electedSet[c.id] = true
	}

	for id, prev := range b.prevRank {
		if !electedSet[id] {
			res.Dropped = append(res.Dropped, DroppedItem{ID: id, PreviousRank: prev})
		}
	}
	sort.Slice(res.Dropped, func(i, j int) bool {
		if res.Dropped[i].PreviousRank != res.Dropped[j].PreviousRank {
			return res.Dropped[i].PreviousRank < res.Dropped[j].PreviousRank
		}
		return res.Dropped[i].ID < res.Dropped[j].ID
	})

	return res
}

// publishLocked makes res the new previous board and updates hysteresis.
func (b *Board) publishLocked(res Result) {
	newPrev := make(map[string]int, len(res.Board))
	newHc := make(map[string]int64, len(res.Board))
	for _, item := range res.Board {
		newPrev[item.ID] = item.Rank
		if item.Score >= b.M {
			newHc[item.ID] = 0
		} else if old, ok := b.hc[item.ID]; ok {
			newHc[item.ID] = old + 1
		} else {
			newHc[item.ID] = 0
		}
	}
	b.prevRank = newPrev
	b.hc = newHc

	// Reclaim zero-score ids that no longer participate.
	for id, s := range b.scores {
		if s == 0 {
			if _, onBoard := newPrev[id]; !onBoard {
				delete(b.scores, id)
			}
		}
	}
}
