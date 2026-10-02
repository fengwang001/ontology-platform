package ontology

import (
	"errors"
	"math/rand/v2"
	"slices"
	"testing"
)

type naiveOracle struct {
	n        int
	edges    []Edge
	sources  map[int]bool
	counts   map[entryKey]int64
	frontier []int64
	version  int64
}

func newNaiveOracle(n int, edges []Edge, sources []int) *naiveOracle {
	oracle := &naiveOracle{
		n:        n,
		edges:    edges,
		sources:  map[int]bool{},
		counts:   map[entryKey]int64{},
		frontier: make([]int64, n),
	}
	for _, source := range sources {
		oracle.sources[source] = true
	}
	for position := range oracle.frontier {
		oracle.frontier[position] = infinity
	}
	return oracle
}

func (oracle *naiveOracle) distances() [][]int64 {
	dist := make([][]int64, oracle.n)
	for from := range dist {
		dist[from] = make([]int64, oracle.n)
		for to := range dist[from] {
			dist[from][to] = infinity
		}
	}
	for _, edge := range oracle.edges {
		if edge.Delay < dist[edge.From][edge.To] {
			dist[edge.From][edge.To] = edge.Delay
		}
	}
	for middle := 0; middle < oracle.n; middle++ {
		for from := 0; from < oracle.n; from++ {
			for to := 0; to < oracle.n; to++ {
				if dist[from][middle] != infinity && dist[middle][to] != infinity &&
					dist[from][middle]+dist[middle][to] < dist[from][to] {
					dist[from][to] = dist[from][middle] + dist[middle][to]
				}
			}
		}
	}
	for position := 0; position < oracle.n; position++ {
		dist[position][position] = 0
	}
	return dist
}

func (oracle *naiveOracle) recompute() []int64 {
	dist := oracle.distances()
	frontier := make([]int64, oracle.n)
	for position := range frontier {
		frontier[position] = infinity
	}
	for entry, count := range oracle.counts {
		if count <= 0 {
			continue
		}
		for target := 0; target < oracle.n; target++ {
			if dist[entry.position][target] != infinity {
				candidate := entry.time + dist[entry.position][target]
				if candidate < frontier[target] {
					frontier[target] = candidate
				}
			}
		}
	}
	return frontier
}

func (oracle *naiveOracle) update(batch []EntryDelta) ([]FrontierChange, error) {
	if len(batch) < 1 || len(batch) > maxBatchSize {
		return nil, ErrInvalidArgument
	}
	for _, item := range batch {
		if item.Position < 0 || item.Position >= oracle.n ||
			item.Time < 0 || item.Time > maxTime ||
			item.Delta == 0 || item.Delta < -maxDelta || item.Delta > maxDelta {
			return nil, ErrInvalidArgument
		}
	}

	net := map[entryKey]int64{}
	for _, item := range batch {
		net[entryKey{position: item.Position, time: item.Time}] += item.Delta
	}
	keys := make([]entryKey, 0, len(net))
	for key := range net {
		keys = append(keys, key)
	}
	sortKeys(keys)

	next := map[entryKey]int64{}
	for _, key := range keys {
		next[key] = oracle.counts[key] + net[key]
		if next[key] > maxCount {
			return nil, ErrInvalidArgument
		}
	}
	for _, key := range keys {
		if next[key] < 0 {
			return nil, rejectionError{kind: ErrNegativeCount, position: key.position, time: key.time}
		}
	}
	for _, key := range keys {
		if net[key] > 0 && !oracle.sources[key.position] && oracle.frontier[key.position] > key.time {
			return nil, rejectionError{kind: ErrCausalityViolation, position: key.position, time: key.time}
		}
	}

	before := slices.Clone(oracle.frontier)
	for key, value := range next {
		if value == 0 {
			delete(oracle.counts, key)
		} else {
			oracle.counts[key] = value
		}
	}
	oracle.frontier = oracle.recompute()
	changes := []FrontierChange{}
	for position := 0; position < oracle.n; position++ {
		if before[position] != oracle.frontier[position] {
			changes = append(changes, FrontierChange{
				Position: position,
				Before:   externalFrontier(before[position]),
				After:    externalFrontier(oracle.frontier[position]),
			})
		}
	}
	oracle.version++
	return changes, nil
}

func TestRandomBatchesAgainstNaiveOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(4242, 99))
	for sequence := 0; sequence < 2000; sequence++ {
		n := 1 + rng.IntN(6)
		edges := []Edge{}
		edgeCount := rng.IntN(9)
		for i := 0; i < edgeCount; i++ {
			from := rng.IntN(n)
			to := rng.IntN(n)
			if to < from {
				from, to = to, from
			}
			delay := int64(rng.IntN(6))
			if from == to && delay == 0 {
				delay = 1
			}
			edges = append(edges, Edge{From: from, To: to, Delay: delay})
		}
		sources := []int{}
		for position := 0; position < n; position++ {
			if rng.IntN(3) == 0 {
				sources = append(sources, position)
			}
		}
		if len(sources) == 0 {
			sources = append(sources, rng.IntN(n))
		}

		tracker, err := NewTracker(n, edges, sources)
		if err != nil {
			t.Fatalf("sequence %d config: %v", sequence, err)
		}
		oracle := newNaiveOracle(n, edges, sources)
		activeKeys := []entryKey{}

		for batchIndex := 0; batchIndex < 24; batchIndex++ {
			batch := []EntryDelta{}
			if rng.IntN(12) == 0 {
				batch = append(batch, EntryDelta{Position: n, Time: 0, Delta: 1})
			} else if len(activeKeys) > 0 && rng.IntN(3) == 0 {
				key := activeKeys[rng.IntN(len(activeKeys))]
				count := oracle.counts[key]
				batch = append(batch, EntryDelta{Position: key.position, Time: key.time, Delta: -1 - rng.Int64N(count)})
			} else {
				itemCount := 1 + rng.IntN(5)
				for i := 0; i < itemCount; i++ {
					position := rng.IntN(n)
					time := int64(rng.IntN(30))
					if !oracle.sources[position] && oracle.frontier[position] != infinity {
						time = oracle.frontier[position] + int64(rng.IntN(3)-1)
					}
					delta := int64(1 + rng.IntN(4))
					if rng.IntN(8) == 0 {
						delta = -delta
					}
					batch = append(batch, EntryDelta{Position: position, Time: time, Delta: delta})
				}
			}

			trackerChanges, trackerErr := tracker.Update(batch)
			oracleChanges, oracleErr := oracle.update(batch)
			decision := "accepted"
			if trackerErr != nil {
				decision = trackerErr.Error()
			}
			t.Logf("sequence=%d batch=%d input=%v decision=%s changes=%v frontiers=%v basis=aggregate;negative;causality;atomic-commit",
				sequence, batchIndex, batch, decision, trackerChanges, frontiersOf(tracker))

			if !sameRejection(trackerErr, oracleErr) {
				t.Fatalf("sequence %d batch %d err tracker=%v oracle=%v input=%v", sequence, batchIndex, trackerErr, oracleErr, batch)
			}
			if !slices.EqualFunc(trackerChanges, oracleChanges, func(left, right FrontierChange) bool {
				return left == right
			}) {
				t.Fatalf("sequence %d batch %d changes tracker=%v oracle=%v", sequence, batchIndex, trackerChanges, oracleChanges)
			}
			trackerVersion, trackerFrontiers := tracker.Frontiers()
			if trackerVersion != oracle.version {
				t.Fatalf("version tracker=%d oracle=%d", trackerVersion, oracle.version)
			}
			wantFrontiers := make([]int64, n)
			for position, value := range oracle.frontier {
				wantFrontiers[position] = externalFrontier(value)
			}
			if !slices.Equal(trackerFrontiers, wantFrontiers) {
				t.Fatalf("frontiers tracker=%v oracle=%v", trackerFrontiers, wantFrontiers)
			}

			activeKeys = activeKeys[:0]
			for key, count := range oracle.counts {
				if count > 0 {
					activeKeys = append(activeKeys, key)
				}
			}
		}
	}
}

func sameRejection(left, right error) bool {
	if left == nil || right == nil {
		return left == right
	}
	var leftRejection rejectionError
	var rightRejection rejectionError
	leftHasKey := errors.As(left, &leftRejection)
	rightHasKey := errors.As(right, &rightRejection)
	if leftHasKey != rightHasKey {
		return false
	}
	if !leftHasKey {
		return errors.Is(left, right)
	}
	return errors.Is(left, right) &&
		leftRejection.position == rightRejection.position &&
		leftRejection.time == rightRejection.time
}

func TestInvalidConfigAndQueries(t *testing.T) {
	if _, err := NewTracker(0, nil, []int{0}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("n=0 err=%v", err)
	}
	tooMany := make([]Edge, maxEdgeCount+1)
	if _, err := NewTracker(2, tooMany, []int{0}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("edge limit err=%v", err)
	}
	tracker, err := NewTracker(1, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Frontier(1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Frontier err=%v", err)
	}
	if _, err := tracker.Complete(0, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Complete err=%v", err)
	}
	if tracker.entryVisits != 0 {
		t.Fatal("initial entry visits")
	}
}
