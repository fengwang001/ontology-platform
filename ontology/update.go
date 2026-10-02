package ontology

import "slices"

const (
	maxBatchSize = 1000
	maxTime      = 1_000_000_000_000
	maxDelta     = 1_000_000
	maxCount     = 1_000_000_000_000
)

type rejectionError struct {
	kind     error
	position int
	time     int64
}

func (err rejectionError) Error() string { return err.kind.Error() }
func (err rejectionError) Unwrap() error { return err.kind }

// Update atomically validates, commits a counter batch, and reports frontier changes.
func (tr *Tracker) Update(batch []EntryDelta) ([]FrontierChange, error) {
	if len(batch) < 1 || len(batch) > maxBatchSize {
		return nil, ErrInvalidArgument
	}
	for _, item := range batch {
		if item.Position < 0 || item.Position >= tr.n ||
			item.Time < 0 || item.Time > maxTime ||
			item.Delta == 0 || item.Delta < -maxDelta || item.Delta > maxDelta {
			return nil, ErrInvalidArgument
		}
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	netByKey := make(map[entryKey]int64, len(batch))
	for _, item := range batch {
		key := entryKey{position: item.Position, time: item.Time}
		netByKey[key] += item.Delta
	}

	keys := make([]entryKey, 0, len(netByKey))
	for key := range netByKey {
		keys = append(keys, key)
	}
	sortKeys(keys)

	for _, key := range keys {
		if tr.counts[key]+netByKey[key] > maxCount {
			return nil, ErrInvalidArgument
		}
	}

	for _, key := range keys {
		if tr.counts[key]+netByKey[key] < 0 {
			return nil, rejectionError{kind: ErrNegativeCount, position: key.position, time: key.time}
		}
	}

	for _, key := range keys {
		net := netByKey[key]
		if net > 0 && !tr.source[key.position] && tr.frontier[key.position] > key.time {
			return nil, rejectionError{kind: ErrCausalityViolation, position: key.position, time: key.time}
		}
	}

	affected := make(map[int]struct{}, len(keys))
	for key, net := range netByKey {
		if net == 0 {
			continue
		}
		next := tr.counts[key] + net
		if next == 0 {
			delete(tr.counts, key)
			tr.active[key.position].remove(key.time)
		} else {
			tr.counts[key] = next
			if next == net {
				tr.active[key.position].add(key.time)
			}
		}
		affected[key.position] = struct{}{}
	}

	before := tr.frontier
	after := tr.recomputeFrontiers(affected)
	changes := make([]FrontierChange, 0, tr.n)
	for position := 0; position < tr.n; position++ {
		if before[position] != after[position] {
			changes = append(changes, FrontierChange{
				Position: position,
				Before:   externalFrontier(before[position]),
				After:    externalFrontier(after[position]),
			})
		}
	}

	tr.frontier = after
	tr.version++
	return changes, nil
}

func (tr *Tracker) recomputeFrontiers(affected map[int]struct{}) []int64 {
	minima := make([]int64, tr.n)
	for position := range minima {
		if _, changed := affected[position]; changed {
			minima[position] = tr.active[position].minimum(tr, position)
		} else {
			minima[position] = tr.active[position].oldMinimum()
		}
	}

	frontiers := make([]int64, tr.n)
	for target := range frontiers {
		frontiers[target] = infinity
		for origin := 0; origin < tr.n; origin++ {
			minimum := minima[origin]
			if minimum == infinity {
				continue
			}
			distance := tr.dist[origin][target]
			if distance == infinity {
				continue
			}
			candidate := minimum + distance
			if candidate < frontiers[target] {
				frontiers[target] = candidate
			}
		}
	}
	return frontiers
}

func sortKeys(keys []entryKey) {
	slices.SortFunc(keys, func(left, right entryKey) int {
		if left.position != right.position {
			return left.position - right.position
		}
		if left.time != right.time {
			if left.time < right.time {
				return -1
			}
			return 1
		}
		return 0
	})
}

func externalFrontier(value int64) int64 {
	if value == infinity {
		return -1
	}
	return value
}

func (set *activeSet) add(time int64) {
	if set.present == nil {
		set.present = make(map[int64]bool)
	}
	if set.present[time] {
		return
	}
	set.present[time] = true
	set.heap = append(set.heap, time)
	set.siftUp(len(set.heap) - 1)
}

func (set *activeSet) remove(time int64) {
	if !set.present[time] {
		return
	}
	delete(set.present, time)
	for len(set.heap) > 0 && !set.present[set.heap[0]] {
		set.removeHeapTop()
	}
}

func (set *activeSet) oldMinimum() int64 {
	for len(set.heap) > 0 {
		if set.present[set.heap[0]] {
			return set.heap[0]
		}
		set.removeHeapTop()
	}
	return infinity
}

func (set *activeSet) minimum(tr *Tracker, position int) int64 {
	for len(set.heap) > 0 {
		time := set.heap[0]
		if !set.present[time] {
			set.removeHeapTop()
			continue
		}
		tr.entryVisits++
		if tr.counts[entryKey{position: position, time: time}] > 0 {
			return time
		}
		delete(set.present, time)
		set.removeHeapTop()
	}
	return infinity
}

func (set *activeSet) removeHeapTop() {
	if len(set.heap) == 0 {
		return
	}
	last := len(set.heap) - 1
	set.heap[0] = set.heap[last]
	set.heap = set.heap[:last]
	if len(set.heap) > 0 {
		set.siftDown(0)
	}
}

func (set *activeSet) siftUp(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if set.heap[parent] <= set.heap[index] {
			return
		}
		set.heap[parent], set.heap[index] = set.heap[index], set.heap[parent]
		index = parent
	}
}

func (set *activeSet) siftDown(index int) {
	for {
		left := index*2 + 1
		if left >= len(set.heap) {
			return
		}
		smallest := left
		right := left + 1
		if right < len(set.heap) && set.heap[right] < set.heap[left] {
			smallest = right
		}
		if set.heap[index] <= set.heap[smallest] {
			return
		}
		set.heap[index], set.heap[smallest] = set.heap[smallest], set.heap[index]
		index = smallest
	}
}
