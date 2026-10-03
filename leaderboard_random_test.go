package ontology

import (
	"math/rand"
	"sort"
	"testing"
)

type naiveLeaderboard struct {
	L, W, M int64
	K, Hs   int

	hasClock bool
	clock    int64
	buckets  map[int64]map[string]int64
	prev     map[string]naivePrev
}

type naivePrev struct {
	rank int
	hc   int
}

func newNaiveLeaderboard(L, W, M int64, K, Hs int) *naiveLeaderboard {
	return &naiveLeaderboard{
		L:       L,
		W:       W,
		M:       M,
		K:       K,
		Hs:      Hs,
		buckets: make(map[int64]map[string]int64),
		prev:    make(map[string]naivePrev),
	}
}

func (m *naiveLeaderboard) add(id string, d, t int64) error {
	if id == "" || d < 1 || d > 1_000_000_000 || t < 0 || t > 1_000_000_000_000_000 {
		return ErrInvalidArgument
	}
	if m.hasClock {
		cur := m.clock / m.L
		if t/m.L <= cur-m.W {
			return ErrExpired
		}
	}

	newClock := t
	if m.hasClock && t < m.clock {
		newClock = m.clock
	}
	cur := newClock / m.L

	score := int64(0)
	for b := cur - m.W + 1; b <= cur; b++ {
		score += m.buckets[b][id]
	}
	if score+d > 1_000_000_000_000_000 {
		return ErrScoreOverflow
	}

	m.hasClock = true
	m.clock = newClock
	if m.buckets[t/m.L] == nil {
		m.buckets[t/m.L] = make(map[string]int64)
	}
	m.buckets[t/m.L][id] += d
	return nil
}

func (m *naiveLeaderboard) snapshot(now int64) (SnapshotResult, error) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return SnapshotResult{}, ErrInvalidArgument
	}
	if m.hasClock && now < m.clock {
		return SnapshotResult{}, ErrClockRolledBack
	}

	m.hasClock = true
	m.clock = now
	result := m.build(now/m.L, true)
	return result, nil
}

func (m *naiveLeaderboard) peek(now int64) (SnapshotResult, error) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return SnapshotResult{}, ErrInvalidArgument
	}
	if m.hasClock && now < m.clock {
		return SnapshotResult{}, ErrClockRolledBack
	}
	return m.build(now/m.L, false), nil
}

func (m *naiveLeaderboard) score(id string) int64 {
	if !m.hasClock {
		return 0
	}
	cur := m.clock / m.L
	total := int64(0)
	for b := cur - m.W + 1; b <= cur; b++ {
		total += m.buckets[b][id]
	}
	return total
}

func (m *naiveLeaderboard) build(cur int64, commit bool) SnapshotResult {
	scores := make(map[string]int64)
	for b := cur - m.W + 1; b <= cur; b++ {
		for id, v := range m.buckets[b] {
			scores[id] += v
		}
	}

	type entry struct {
		id    string
		score int64
		rank  int
	}

	var all []entry
	for id, old := range m.prev {
		cutoff := m.M
		if old.hc < m.Hs {
			cutoff = m.M - m.M/4
		}
		if scores[id] >= cutoff {
			all = append(all, entry{id: id, score: scores[id]})
		}
	}
	for id, score := range scores {
		if _, exists := m.prev[id]; !exists && score >= m.M {
			all = append(all, entry{id: id, score: score})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].id < all[j].id
	})

	for i := range all {
		if i == 0 || all[i].score != all[i-1].score {
			all[i].rank = i + 1
		} else {
			all[i].rank = all[i-1].rank
		}
	}

	limit := len(all)
	if limit > m.K {
		limit = m.K
	}

	result := SnapshotResult{
		Items:   make([]RankedItem, 0, limit),
		Dropped: make([]DroppedItem, 0),
	}
	selected := make(map[string]entry, limit)

	for _, item := range all[:limit] {
		ranked := RankedItem{ID: item.id, Score: item.score, Rank: item.rank, New: true}
		if old, exists := m.prev[item.id]; exists {
			ranked.Change = old.rank - item.rank
			ranked.New = false
		}
		result.Items = append(result.Items, ranked)
		selected[item.id] = item
	}

	for id, old := range m.prev {
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
		next := make(map[string]naivePrev, limit)
		for id, item := range selected {
			hc := 0
			if old, exists := m.prev[id]; exists && item.score < m.M {
				hc = old.hc + 1
			}
			next[id] = naivePrev{rank: item.rank, hc: hc}
		}
		m.prev = next
	}

	return result
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		L := int64(1 + rng.Intn(10))
		W := int64(1 + rng.Intn(8))
		M := int64(1 + rng.Intn(120))
		K := 1 + rng.Intn(6)
		Hs := 1 + rng.Intn(4)

		lb, err := NewLeaderboard(L, W, M, K, Hs)
		if err != nil {
			t.Fatalf("seed=%d params: %v", seed, err)
		}
		model := newNaiveLeaderboard(L, W, M, K, Hs)

		for step := 0; step < 70; step++ {
			id := string(rune('a' + rng.Intn(7)))

			switch rng.Intn(10) {
			case 0, 1, 2, 3:
				at := rng.Int63n(260)
				if model.hasClock && rng.Intn(4) == 0 {
					at = model.clock - rng.Int63n(W*L+6)
					if at < 0 {
						at = 0
					}
				}
				d := int64(1 + rng.Intn(140))
				gotErr := lb.Add(id, d, at)
				wantErr := model.add(id, d, at)
				t.Logf("seed=%d step=%d params(L=%d W=%d M=%d K=%d Hs=%d) Add(%q,%d,%d) => got=%v want=%v; basis: bucket=%d cur=%d low=%d",
					seed, step, L, W, M, K, Hs, id, d, at, gotErr, wantErr, at/L, maxBucket(model), safeLow(model))
				if gotErr != wantErr {
					t.Fatalf("seed=%d Add mismatch", seed)
				}
			case 4, 5:
				now := rng.Int63n(280)
				if model.hasClock && rng.Intn(5) == 0 {
					now = model.clock - 1
				}
				gotResult, gotErr := lb.Snapshot(now)
				wantResult, wantErr := model.snapshot(now)
				t.Logf("seed=%d step=%d Snapshot(%d) => got=(%+v,%v) want=(%+v,%v); basis: cur=%d window=(%d,%d]",
					seed, step, now, gotResult.Items, gotErr, wantResult.Items, wantErr, now/L, now/L-W, now/L)
				if gotErr != wantErr || !sameResult(gotResult, wantResult) {
					t.Fatalf("seed=%d Snapshot mismatch", seed)
				}
			case 6:
				now := rng.Int63n(280)
				gotResult, gotErr := lb.Peek(now)
				wantResult, wantErr := model.peek(now)
				t.Logf("seed=%d step=%d Peek(%d) => got=(%+v,%v) want=(%+v,%v); basis: read-only cur=%d",
					seed, step, now, gotResult.Items, gotErr, wantResult.Items, wantErr, model.clock)
				if gotErr != wantErr || !sameResult(gotResult, wantResult) {
					t.Fatalf("seed=%d Peek mismatch", seed)
				}
			default:
				got := lb.Score(id)
				want := model.score(id)
				t.Logf("seed=%d step=%d Score(%q) => got=%d want=%d; basis: sum buckets in (%d,%d]",
					seed, step, id, got, want, safeLow(model), safeCur(model))
				if got != want {
					t.Fatalf("seed=%d Score mismatch", seed)
				}
			}
		}
	}
}

func sameResult(got, want SnapshotResult) bool {
	if len(got.Items) != len(want.Items) || len(got.Dropped) != len(want.Dropped) {
		return false
	}
	for i := range got.Items {
		if got.Items[i] != want.Items[i] {
			return false
		}
	}
	for i := range got.Dropped {
		if got.Dropped[i] != want.Dropped[i] {
			return false
		}
	}
	return true
}

func maxBucket(m *naiveLeaderboard) int64 {
	if !m.hasClock {
		return -1
	}
	return m.clock / m.L
}

func safeCur(m *naiveLeaderboard) int64 {
	if !m.hasClock {
		return -1
	}
	return m.clock / m.L
}

func safeLow(m *naiveLeaderboard) int64 {
	if !m.hasClock {
		return 0
	}
	cur := m.clock / m.L
	return cur - m.W
}
