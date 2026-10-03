package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrExpired         = errors.New("event expired")
	ErrClockRolledBack = errors.New("clock rolled back")
	ErrScoreOverflow   = errors.New("score overflow")
)

type RankedItem struct {
	ID     string
	Score  int64
	Rank   int
	Change int
	New    bool
}

type DroppedItem struct {
	ID       string
	PrevRank int
}

type SnapshotResult struct {
	Items   []RankedItem
	Dropped []DroppedItem
}

type Leaderboard struct {
	mu sync.RWMutex

	L  int64
	W  int64
	M  int64
	K  int
	Hs int

	hasClock bool
	clock    int64

	buckets map[int64]*bucketEntry
	order   []*bucketEntry
	scores  map[string]int64

	prev map[string]prevRank

	addBucketLookups      int
	expiredBucketPops     int
	expiredScoreUpdates   int
	windowBucketRangeScan int
}

type bucketEntry struct {
	bucket int64
	scores map[string]int64
}

type prevRank struct {
	rank int
	hc   int
}

type candidate struct {
	id    string
	score int64
	rank  int
}

func NewLeaderboard(L, W, M int64, K, Hs int) (*Leaderboard, error) {
	if L < 1 || L > 1_000_000_000 ||
		W < 1 || W > 1000 ||
		M < 1 || M > 1_000_000_000_000 ||
		K < 1 || K > 100 ||
		Hs < 1 || Hs > 100 {
		return nil, ErrInvalidArgument
	}

	return &Leaderboard{
		L:       L,
		W:       W,
		M:       M,
		K:       K,
		Hs:      Hs,
		buckets: make(map[int64]*bucketEntry),
		scores:  make(map[string]int64),
		prev:    make(map[string]prevRank),
	}, nil
}

func (lb *Leaderboard) Add(id string, d, t int64) error {
	if id == "" || d < 1 || d > 1_000_000_000 || t < 0 || t > 1_000_000_000_000_000 {
		return ErrInvalidArgument
	}

	lb.mu.Lock()
	defer lb.mu.Unlock()

	if lb.hasClock {
		currentBucket := lb.clock / lb.L
		if t/lb.L <= currentBucket-lb.W {
			return ErrExpired
		}
	}

	newClock := t
	if lb.hasClock && t < lb.clock {
		newClock = lb.clock
	}

	popped, oldScores := lb.advanceWindowLocked(newClock / lb.L)
	newScore := lb.scores[id] + d
	if newScore > 1_000_000_000_000_000 {
		lb.rollbackWindowLocked(popped, oldScores)
		return ErrScoreOverflow
	}

	lb.hasClock = true
	lb.clock = newClock

	bucket := t / lb.L
	entry := lb.buckets[bucket]
	if entry == nil {
		entry = &bucketEntry{
			bucket: bucket,
			scores: make(map[string]int64),
		}
		lb.buckets[bucket] = entry
		lb.pushBucketLocked(entry)
	}
	lb.addBucketLookups++
	entry.scores[id] += d
	lb.scores[id] = newScore

	return nil
}

func (lb *Leaderboard) Snapshot(now int64) (SnapshotResult, error) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return SnapshotResult{}, ErrInvalidArgument
	}

	lb.mu.Lock()
	defer lb.mu.Unlock()

	if lb.hasClock && now < lb.clock {
		return SnapshotResult{}, ErrClockRolledBack
	}

	lb.hasClock = true
	lb.clock = now
	lb.advanceWindowLocked(now / lb.L)

	result := lb.buildLocked(lb.scores, lb.prev, true)
	return result, nil
}

func (lb *Leaderboard) Peek(now int64) (SnapshotResult, error) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return SnapshotResult{}, ErrInvalidArgument
	}

	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if lb.hasClock && now < lb.clock {
		return SnapshotResult{}, ErrClockRolledBack
	}

	projected := lb.projectedScoresLocked(now / lb.L)
	return lb.buildLocked(projected, lb.prev, false), nil
}

func (lb *Leaderboard) Score(id string) int64 {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	return lb.scores[id]
}

func (lb *Leaderboard) pushBucketLocked(entry *bucketEntry) {
	lb.order = append(lb.order, entry)
	index := len(lb.order) - 1
	for index > 0 {
		parent := (index - 1) / 2
		if lb.order[parent].bucket <= lb.order[index].bucket {
			break
		}
		lb.order[parent], lb.order[index] = lb.order[index], lb.order[parent]
		index = parent
	}
}

func (lb *Leaderboard) popBucketLocked() *bucketEntry {
	n := len(lb.order)
	entry := lb.order[0]
	last := n - 1
	if n == 1 {
		lb.order = nil
		return entry
	}
	lb.order[0] = lb.order[last]
	lb.order = lb.order[:last]

	index := 0
	for {
		left := index*2 + 1
		right := left + 1
		smallest := index
		if left < len(lb.order) && lb.order[left].bucket < lb.order[smallest].bucket {
			smallest = left
		}
		if right < len(lb.order) && lb.order[right].bucket < lb.order[smallest].bucket {
			smallest = right
		}
		if smallest == index {
			break
		}
		lb.order[index], lb.order[smallest] = lb.order[smallest], lb.order[index]
		index = smallest
	}

	return entry
}

func (lb *Leaderboard) advanceWindowLocked(currentBucket int64) ([]*bucketEntry, map[string]int64) {
	oldScores := make(map[string]int64)
	var popped []*bucketEntry

	for len(lb.order) > 0 && lb.order[0].bucket <= currentBucket-lb.W {
		entry := lb.popBucketLocked()
		delete(lb.buckets, entry.bucket)
		popped = append(popped, entry)
		lb.expiredBucketPops++

		for id, delta := range entry.scores {
			if _, exists := oldScores[id]; !exists {
				oldScores[id] = lb.scores[id]
			}
			next := lb.scores[id] - delta
			if next == 0 {
				delete(lb.scores, id)
			} else {
				lb.scores[id] = next
			}
			lb.expiredScoreUpdates++
		}
	}

	return popped, oldScores
}

func (lb *Leaderboard) rollbackWindowLocked(popped []*bucketEntry, oldScores map[string]int64) {
	for i := len(popped) - 1; i >= 0; i-- {
		entry := popped[i]
		lb.buckets[entry.bucket] = entry
		lb.pushBucketLocked(entry)
	}

	for id, score := range oldScores {
		lb.scores[id] = score
	}

	lb.expiredBucketPops -= len(popped)
	updateCount := 0
	for _, entry := range popped {
		updateCount += len(entry.scores)
	}
	lb.expiredScoreUpdates -= updateCount
}

func (lb *Leaderboard) projectedScoresLocked(currentBucket int64) map[string]int64 {
	projected := make(map[string]int64, len(lb.scores))
	for id, score := range lb.scores {
		projected[id] = score
	}

	for _, entry := range lb.order {
		if entry.bucket > currentBucket-lb.W {
			continue
		}
		for id, delta := range entry.scores {
			next := projected[id] - delta
			if next == 0 {
				delete(projected, id)
			} else {
				projected[id] = next
			}
		}
	}

	return projected
}

func (lb *Leaderboard) buildLocked(scores map[string]int64, previous map[string]prevRank, commit bool) SnapshotResult {
	candidates := make([]candidate, 0, len(scores)+len(previous))
	seen := make(map[string]struct{}, len(scores)+len(previous))

	for id := range previous {
		score := scores[id]
		cutoff := lb.M
		if previous[id].hc < lb.Hs {
			cutoff = lb.M - lb.M/4
		}
		if score >= cutoff {
			candidates = append(candidates, candidate{id: id, score: score})
		}
		seen[id] = struct{}{}
	}

	for id, score := range scores {
		if _, exists := seen[id]; exists {
			continue
		}
		if score >= lb.M {
			candidates = append(candidates, candidate{id: id, score: score})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].id < candidates[j].id
	})

	for i := range candidates {
		if i == 0 || candidates[i].score != candidates[i-1].score {
			candidates[i].rank = i + 1
		} else {
			candidates[i].rank = candidates[i-1].rank
		}
	}

	limit := len(candidates)
	if limit > lb.K {
		limit = lb.K
	}

	result := SnapshotResult{
		Items:   make([]RankedItem, 0, limit),
		Dropped: make([]DroppedItem, 0),
	}
	selected := make(map[string]candidate, limit)

	for _, entry := range candidates[:limit] {
		item := RankedItem{
			ID:    entry.id,
			Score: entry.score,
			Rank:  entry.rank,
			New:   true,
		}
		if old, exists := previous[entry.id]; exists {
			item.Change = old.rank - entry.rank
			item.New = false
		}
		result.Items = append(result.Items, item)
		selected[entry.id] = entry
	}

	for id, old := range previous {
		if _, exists := selected[id]; !exists {
			result.Dropped = append(result.Dropped, DroppedItem{ID: id, PrevRank: old.rank})
		}
	}
	sort.Slice(result.Dropped, func(i, j int) bool {
		if result.Dropped[i].PrevRank != result.Dropped[j].PrevRank {
			return result.Dropped[i].PrevRank < result.Dropped[j].PrevRank
		}
		return result.Dropped[i].ID < result.Dropped[j].ID
	})

	if commit {
		next := make(map[string]prevRank, limit)
		for id, entry := range selected {
			hc := 0
			if old, exists := previous[id]; exists && entry.score < lb.M {
				hc = old.hc + 1
			}
			next[id] = prevRank{rank: entry.rank, hc: hc}
		}
		lb.prev = next
	}

	return result
}
