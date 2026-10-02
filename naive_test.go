package ontology

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

type naiveTracker struct {
	n         int
	edges     []Edge
	sources   map[int]bool
	distances [][]int64
	ledger    map[naiveKey]uint64
	version   uint64
}

type naiveKey struct {
	position int
	time     int64
}

type naiveResult struct {
	version   uint64
	frontiers []int64
	changes   []FrontierChange
	rejection Rejection
}

func newNaiveTracker(n int, edges []Edge, sources []int) (*naiveTracker, error) {
	if n < 1 || n > 64 || len(edges) > 512 || len(sources) == 0 {
		return nil, ErrInvalidConfig
	}

	sourceSet := make(map[int]bool, len(sources))
	for _, source := range sources {
		if source < 0 || source >= n || sourceSet[source] {
			return nil, ErrInvalidConfig
		}
		sourceSet[source] = true
	}

	distances := make([][]int64, n)
	for from := range distances {
		distances[from] = make([]int64, n)
		for to := range distances[from] {
			distances[from][to] = infinity
		}
	}

	for _, edge := range edges {
		if edge.From < 0 || edge.From >= n || edge.To < 0 || edge.To >= n ||
			edge.Delay < 0 || edge.Delay > 1_000_000 {
			return nil, ErrInvalidConfig
		}
		if current := distances[edge.From][edge.To]; current == infinity || edge.Delay < current {
			distances[edge.From][edge.To] = edge.Delay
		}
	}

	for middle := 0; middle < n; middle++ {
		for from := 0; from < n; from++ {
			for to := 0; to < n; to++ {
				if distances[from][middle] != infinity && distances[middle][to] != infinity {
					candidate := distances[from][middle] + distances[middle][to]
					if distances[from][to] == infinity || candidate < distances[from][to] {
						distances[from][to] = candidate
					}
				}
			}
		}
	}

	for position := 0; position < n; position++ {
		if distances[position][position] == 0 {
			return nil, ErrInvalidConfig
		}
	}

	for position := 0; position < n; position++ {
		distances[position][position] = 0
	}

	return &naiveTracker{
		n:         n,
		edges:     append([]Edge(nil), edges...),
		sources:   sourceSet,
		distances: distances,
		ledger:    make(map[naiveKey]uint64),
	}, nil
}

func (m *naiveTracker) update(batch []Delta) naiveResult {
	if len(batch) == 0 || len(batch) > 1000 {
		return naiveResult{version: m.version, rejection: Rejection{Reason: ErrInvalidArgument}}
	}

	netByKey := make(map[naiveKey]int64, len(batch))
	keys := make([]naiveKey, 0, len(batch))
	for _, delta := range batch {
		if delta.Position < 0 || delta.Position >= m.n ||
			delta.Time < 0 || delta.Time > 1_000_000_000_000 ||
			delta.Amount == 0 || delta.Amount < -1_000_000 || delta.Amount > 1_000_000 {
			return naiveResult{version: m.version, rejection: Rejection{Reason: ErrInvalidArgument}}
		}

		key := naiveKey{position: delta.Position, time: delta.Time}
		if _, seen := netByKey[key]; !seen {
			keys = append(keys, key)
		}
		netByKey[key] += delta.Amount
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].position != keys[j].position {
			return keys[i].position < keys[j].position
		}
		return keys[i].time < keys[j].time
	})

	before := m.allFrontiers()
	nonZero := make([]naiveKey, 0, len(keys))
	for _, key := range keys {
		net := netByKey[key]
		if net == 0 {
			continue
		}
		next := int64(m.ledger[key]) + net
		if next > 1_000_000_000_000 {
			return naiveResult{version: m.version, frontiers: before, rejection: Rejection{Reason: ErrInvalidArgument}}
		}
		nonZero = append(nonZero, key)
	}

	for _, key := range nonZero {
		if int64(m.ledger[key])+netByKey[key] < 0 {
			return naiveResult{
				version:   m.version,
				frontiers: before,
				rejection: Rejection{Reason: ErrNegativeCount, Position: key.position, Time: key.time},
			}
		}
	}

	for _, key := range nonZero {
		if netByKey[key] > 0 && !m.sources[key.position] &&
			(before[key.position] == infinity || before[key.position] > key.time) {
			return naiveResult{
				version:   m.version,
				frontiers: before,
				rejection: Rejection{Reason: ErrCausalViolation, Position: key.position, Time: key.time},
			}
		}
	}

	for _, key := range nonZero {
		next := int64(m.ledger[key]) + netByKey[key]
		if next == 0 {
			delete(m.ledger, key)
		} else {
			m.ledger[key] = uint64(next)
		}
	}

	after := m.allFrontiers()
	changes := make([]FrontierChange, 0)
	for position := 0; position < m.n; position++ {
		if before[position] != after[position] {
			changes = append(changes, FrontierChange{Position: position, Before: before[position], After: after[position]})
		}
	}

	m.version++
	return naiveResult{version: m.version, frontiers: after, changes: changes}
}

func (m *naiveTracker) allFrontiers() []int64 {
	frontiers := make([]int64, m.n)
	for target := range frontiers {
		frontiers[target] = infinity
	}

	for key := range m.ledger {
		if m.ledger[key] == 0 {
			continue
		}
		for target := 0; target < m.n; target++ {
			delay := m.distances[key.position][target]
			if delay == infinity {
				continue
			}
			candidate := key.time + delay
			if frontiers[target] == infinity || candidate < frontiers[target] {
				frontiers[target] = candidate
			}
		}
	}

	return frontiers
}

func TestRandomComparisonSkeleton(t *testing.T) {
	model, err := newNaiveTracker(1, nil, []int{0})
	if err != nil || model == nil {
		t.Fatalf("newNaiveTracker() = (%v, %v)", model, err)
	}

	if got := model.update(nil); !errors.Is(got.rejection.Reason, ErrInvalidArgument) {
		t.Fatalf("naive update = %#v", got)
	}

	_ = rand.New(rand.NewPCG(1, 2))
	_ = reflect.DeepEqual
}

func TestRandomBatchSequencesMatchNaive(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x51a7ed_1170, 0xb17c_2026))

	for group := 0; group < 2000; group++ {
		n := 1 + rng.IntN(4)
		edges := generateEdges(rng, n)
		sources := generateSources(rng, n)

		tracker, trackerErr := NewTracker(n, edges, sources)
		model, modelErr := newNaiveTracker(n, edges, sources)
		if (trackerErr == nil) != (modelErr == nil) {
			t.Fatalf("group %d config disagreement: real=%v naive=%v n=%d edges=%v sources=%v",
				group, trackerErr, modelErr, n, edges, sources)
		}
		if trackerErr != nil {
			t.Logf("group=%d input config={n:%d edges:%v sources:%v} output=rejected basis=%v",
				group, n, edges, sources, trackerErr)
			continue
		}

		for step := 0; step < 5; step++ {
			batch := generateBatch(rng, n)
			version, changes, rejection := tracker.Update(batch)
			result := model.update(batch)
			_, actualFrontiers := tracker.Frontiers()
			if result.rejection.Reason != nil && result.frontiers == nil {
				result.frontiers = model.allFrontiers()
			}

			basis := "accepted"
			switch {
			case errors.Is(rejection.Reason, ErrInvalidArgument):
				basis = "invalid argument checked before count and causal validation"
			case errors.Is(rejection.Reason, ErrNegativeCount):
				basis = "net count would be negative"
			case errors.Is(rejection.Reason, ErrCausalViolation):
				basis = "pre-update frontier is after timestamp"
			}

			t.Logf("group=%d step=%d input=%v output={ver:%d frontiers:%v changes:%v rejection:{reason:%v pos:%d t:%d}} basis=%s",
				group, step, batch, version, actualFrontiers, changes,
				rejection.Reason, rejection.Position, rejection.Time, basis)

			if version != result.version ||
				!reflect.DeepEqual(actualFrontiers, result.frontiers) ||
				!reflect.DeepEqual(changes, result.changes) ||
				!sameRejection(rejection, result.rejection) {
				t.Fatalf("group %d step %d mismatch: real=(%d,%v,%#v) naive=(%d,%v,%#v)",
					group, step, version, actualFrontiers, rejection,
					result.version, result.frontiers, result.rejection)
			}

			if !reflect.DeepEqual(trackerLedgerForNaive(tracker), model.ledger) {
				t.Fatalf("group %d step %d ledger mismatch: real=%v naive=%v",
					group, step, trackerLedgerForNaive(tracker), model.ledger)
			}
		}
	}
}

func generateEdges(rng *rand.Rand, n int) []Edge {
	edges := make([]Edge, rng.IntN(9))
	for i := range edges {
		edges[i] = Edge{
			From:  rng.IntN(n),
			To:    rng.IntN(n),
			Delay: int64(rng.IntN(4)),
		}
	}
	return edges
}

func generateSources(rng *rand.Rand, n int) []int {
	count := 1 + rng.IntN(n)
	sources := make([]int, count)
	for i := range sources {
		sources[i] = rng.IntN(n)
	}
	return sources
}

func generateBatch(rng *rand.Rand, n int) []Delta {
	if rng.IntN(20) == 0 {
		return nil
	}

	batch := make([]Delta, 1+rng.IntN(5))
	for i := range batch {
		position := rng.IntN(n + 1)
		time := int64(rng.IntN(12))
		if rng.IntN(10) == 0 {
			time = 1_000_000_000_001
		}
		amount := int64(1 + rng.IntN(4))
		if rng.IntN(2) == 0 {
			amount = -amount
		}
		if rng.IntN(10) == 0 {
			amount = 0
		}

		batch[i] = Delta{Position: position, Time: time, Amount: amount}

		if rng.IntN(3) == 0 && len(batch) > 0 {
			batch[i] = batch[rng.IntN(i+1)]
			batch[i].Amount = -amount
		}
	}
	return batch
}

func sameRejection(actual, expected Rejection) bool {
	if (actual.Reason == nil) != (expected.Reason == nil) {
		return false
	}
	if actual.Reason != nil {
		if !errors.Is(actual.Reason, expected.Reason) {
			return false
		}
	}
	return actual.Position == expected.Position && actual.Time == expected.Time
}

func trackerLedgerForNaive(tracker *Tracker) map[naiveKey]uint64 {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	ledger := make(map[naiveKey]uint64, len(tracker.ledger))
	for key, count := range tracker.ledger {
		ledger[naiveKey{position: key.position, time: key.time}] = count
	}
	return ledger
}
