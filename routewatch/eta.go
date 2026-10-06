package routewatch

// publisher owns the externally released ETAs and the debounce / lock rules.
// values is indexed by stop index; a missing entry means "never published".
// Keeping the published value independent from the simulated result is what
// makes prefix immutability observable: republish only mutates entries at or
// after from.
type publisher struct {
	debounce int64
	lock     int64
	values   map[int]int64
}

func newPublisher(debounce, lock int64, n int) *publisher {
	return &publisher{
		debounce: debounce,
		lock:     lock,
		values:   make(map[int]int64, n),
	}
}

// republish reconciles published ETAs with fresh simulation results.
//
//   - Arrived (reported), skipped or canceled stops lose their publication.
//   - A stop that was never published receives the simulated ETA verbatim
//     (the first publication is not debounced or locked).
//   - Otherwise the value changes only when |new-old| is strictly greater
//     than the debounce threshold (equality holds the old value) and the ETA
//     is more than the lock window away from the current clock (equality with
//     the lock window means "inside" and freezes the value).
func (p *publisher) republish(results []StopResult, reported map[int]bool, clock int64, from int) {
	for i := from; i < len(results); i++ {
		r := results[i]
		if reported[i] || r.Canceled || !r.Valid {
			delete(p.values, i)
			continue
		}
		old, existed := p.values[i]
		if !existed {
			p.values[i] = r.Arrival
			continue
		}
		insideLock := r.Arrival-clock <= p.lock
		if !insideLock && absDelta(r.Arrival, old) > p.debounce {
			p.values[i] = r.Arrival
		}
	}
}

// list returns publications in stop order, omitting non-arrived stops only.
func (p *publisher) list(results []StopResult) []PublishedETA {
	out := make([]PublishedETA, 0, len(p.values))
	for i := range results {
		if eta, ok := p.values[i]; ok {
			out = append(out, PublishedETA{Index: i, ID: results[i].ID, ETA: eta})
		}
	}
	return out
}
