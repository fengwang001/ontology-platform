package ontology

import "sort"

func (t *Tracker) Update(batch []Delta) (uint64, []FrontierChange, Rejection) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(batch) == 0 || len(batch) > 1000 {
		return t.version, nil, Rejection{Reason: ErrInvalidArgument}
	}

	netByKey := make(map[ledgerKey]int64, len(batch))
	keys := make([]ledgerKey, 0, len(batch))
	for _, delta := range batch {
		if delta.Position < 0 || delta.Position >= t.n ||
			delta.Time < 0 || delta.Time > 1_000_000_000_000 ||
			delta.Amount == 0 || delta.Amount < -1_000_000 || delta.Amount > 1_000_000 {
			return t.version, nil, Rejection{Reason: ErrInvalidArgument}
		}

		key := ledgerKey{position: delta.Position, time: delta.Time}
		if _, seen := netByKey[key]; !seen {
			keys = append(keys, key)
		}
		netByKey[key] += delta.Amount
	}

	sortLedgerKeys(keys)
	changes, rejection := t.validateAndApply(keys, netByKey)
	return t.version, changes, rejection
}

func (t *Tracker) validateAndApply(keys []ledgerKey, netByKey map[ledgerKey]int64) ([]FrontierChange, Rejection) {
	nonZeroKeys := make([]ledgerKey, 0, len(keys))
	for _, key := range keys {
		net := netByKey[key]
		if net == 0 {
			continue
		}

		current := int64(t.ledger[key])
		next := current + net
		if next > 1_000_000_000_000 {
			return nil, Rejection{Reason: ErrInvalidArgument}
		}
		nonZeroKeys = append(nonZeroKeys, key)
	}

	for _, key := range nonZeroKeys {
		next := int64(t.ledger[key]) + netByKey[key]
		if next < 0 {
			return nil, Rejection{Reason: ErrNegativeCount, Position: key.position, Time: key.time}
		}
	}

	for _, key := range nonZeroKeys {
		if netByKey[key] > 0 {
			if _, isSource := t.sources[key.position]; !isSource &&
				(t.frontiers[key.position] == infinity || t.frontiers[key.position] > key.time) {
				return nil, Rejection{Reason: ErrCausalViolation, Position: key.position, Time: key.time}
			}
		}
	}

	before := append([]int64(nil), t.frontiers...)
	for _, key := range nonZeroKeys {
		current := int64(t.ledger[key])
		next := current + netByKey[key]
		if next == 0 {
			delete(t.ledger, key)
		} else {
			if current == 0 {
				t.active.add(key.position, key.time)
			}
			t.ledger[key] = uint64(next)
		}
	}

	for position := 0; position < t.n; position++ {
		t.frontiers[position] = t.computeFrontier(position)
	}

	changes := make([]FrontierChange, 0)
	for position := 0; position < t.n; position++ {
		if before[position] != t.frontiers[position] {
			changes = append(changes, FrontierChange{
				Position: position,
				Before:   before[position],
				After:    t.frontiers[position],
			})
		}
	}

	t.version++
	t.entryHits += uint64(len(nonZeroKeys))
	return changes, Rejection{}
}

func (t *Tracker) computeFrontier(target int) int64 {
	result := infinity
	for position := 0; position < t.n; position++ {
		delay := t.distance[position][target]
		if delay == -1 {
			continue
		}

		minimum, ok := t.active.minimum(position, func(time int64) bool {
			return t.ledger[ledgerKey{position: position, time: time}] > 0
		})
		if !ok {
			continue
		}

		candidate := minimum + delay
		if result == infinity || candidate < result {
			result = candidate
		}
	}
	return result
}

func sortLedgerKeys(keys []ledgerKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].position != keys[j].position {
			return keys[i].position < keys[j].position
		}
		return keys[i].time < keys[j].time
	})
}
