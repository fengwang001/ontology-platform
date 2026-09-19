package ontology

import "time"

// Put records that entity's attribute equals value on valid interval
// [from, to) (zero to = forever), as known at transaction time at.
//
// Superseded facts are never deleted: their Tx interval is closed at at and
// their uncovered valid fragments survive as new records. All mutations for
// one entity happen under a single lock, so AsOf never observes a half-done
// clipping.
func (s *Store) Put(entity, attribute, value string, from, to, at time.Time) error {
	if err := validateWrite(from, to, at); err != nil {
		return err
	}

	sh := s.shard(entity)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	st := s.stateLocked(sh, entity)
	if !st.lastWrite.IsZero() && !at.After(st.lastWrite) {
		return ErrTxNotAdvancing
	}

	nv := Interval{From: from, To: to}
	existing := st.attrs[attribute]
	next := make([]*Record, 0, len(existing)+2)

	for _, r := range existing {
		if !r.Tx.To.IsZero() || !nv.Overlaps(r.Valid) {
			next = append(next, r)
			continue
		}

		// Close the old fact's transaction interval at at.
		closed := *r
		closed.Tx.To = at
		next = append(next, &closed)

		// Left residual [old.From, max(old.From, nv.From)).
		leftTo := maxTime(r.Valid.From, nv.From)
		if r.Valid.From.Before(leftTo) {
			next = append(next, &Record{
				Valid: Interval{From: r.Valid.From, To: leftTo},
				Tx:    Interval{From: at, To: time.Time{}},
				Value: r.Value,
			})
		}

		// Right residual [min(old.To, nv.To), old.To).
		rightFrom := minTime(r.Valid.To, nv.To)
		if rightFrom.IsZero() {
			continue
		}
		if r.Valid.To.IsZero() || rightFrom.Before(r.Valid.To) {
			next = append(next, &Record{
				Valid: Interval{From: rightFrom, To: r.Valid.To},
				Tx:    Interval{From: at, To: time.Time{}},
				Value: r.Value,
			})
		}
	}

	next = append(next, &Record{
		Valid: nv,
		Tx:    Interval{From: at, To: time.Time{}},
		Value: value,
	})

	st.attrs[attribute] = next
	st.lastWrite = at
	return nil
}
