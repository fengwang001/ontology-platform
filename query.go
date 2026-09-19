package ontology

import (
	"sort"
	"time"
)

// AttrValue is the result of an AsOf query.
type AttrValue struct {
	Value  string
	Status LookupStatus
}

func (s *Store) snapshot(entity, attribute string) ([]*Record, bool) {
	sh := s.shard(entity)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	st := sh.store[entity]
	if st == nil || len(st.attrs[attribute]) == 0 {
		return nil, false
	}
	rs := st.attrs[attribute]
	out := make([]*Record, len(rs))
	copy(out, rs)
	return out, true
}

// AsOf returns the value of entity's attribute valid at validAt and known at
// txAt. The LookupStatus distinguishes no facts ever, validAt outside every
// interval, and the fact not yet known at txAt.
func (s *Store) AsOf(entity, attribute string, validAt, txAt time.Time) AttrValue {
	rs, ok := s.snapshot(entity, attribute)
	if !ok {
		return AttrValue{Status: StatusNoFacts}
	}

	validMatch := false
	var best *Record
	for _, r := range rs {
		if !r.Valid.Contains(validAt) {
			continue
		}
		validMatch = true
		if !r.Tx.Contains(txAt) {
			continue
		}
		if best == nil || r.Tx.From.After(best.Tx.From) {
			best = r
		}
	}
	if best != nil {
		return AttrValue{Value: best.Value, Status: StatusFound}
	}
	if validMatch {
		return AttrValue{Status: StatusNotYetKnown}
	}
	return AttrValue{Status: StatusOutsideValidity}
}

// Trajectory returns every value the attribute had at validAt, ordered
// strictly by the transaction time at which each version became known.
func (s *Store) Trajectory(entity, attribute string, validAt time.Time) []Record {
	rs, ok := s.snapshot(entity, attribute)
	if !ok {
		return nil
	}
	var out []Record
	for _, r := range rs {
		if r.Valid.Contains(validAt) {
			out = append(out, *r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Tx.From.Equal(out[j].Tx.From) {
			return out[i].Tx.From.Before(out[j].Tx.From)
		}
		return out[i].Valid.From.Before(out[j].Valid.From)
	})
	return out
}

// CurrentRecords returns the open-transaction records for an attribute,
// ordered by valid From.
func (s *Store) CurrentRecords(entity, attribute string) []Record {
	rs, ok := s.snapshot(entity, attribute)
	if !ok {
		return nil
	}
	var out []Record
	for _, r := range rs {
		if r.Tx.To.IsZero() {
			out = append(out, *r)
		}
	}
	sortByValidFrom(out)
	return out
}
