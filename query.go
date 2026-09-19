package ontology

import (
	"sort"
	"time"
)

// AsOf returns the fact for entity's property that was valid at validAt
// and known (not yet superseded) at txAt.
//
// The three "not found" situations are reported as distinct sentinel
// errors: ErrNoFacts (nothing was ever written for this entity/property),
// ErrValidOutOfRange (validAt is outside every known valid interval), and
// ErrNotYetKnown (validAt is covered, but only by facts the system did not
// know yet at txAt).
func (s *Store) AsOf(entity, prop string, validAt, txAt time.Time) (Fact, error) {
	es := s.entity(entity)
	es.mu.RLock()
	defer es.mu.RUnlock()
	facts := es.props[prop]
	if len(facts) == 0 {
		return Fact{}, ErrNoFacts
	}
	covered := false
	for _, f := range facts {
		if !contains(f.ValidFrom, f.ValidTo, validAt) {
			continue
		}
		covered = true
		if f.visibleAt(txAt) {
			return *f, nil
		}
	}
	if !covered {
		return Fact{}, ErrValidOutOfRange
	}
	return Fact{}, ErrNotYetKnown
}

// Corrections returns the full correction trajectory of entity's property
// at validAt: every value the system ever believed for that point, ordered
// by transaction time (stable, strictly increasing TxFrom). It returns
// ErrNoFacts or ErrValidOutOfRange in the same situations as AsOf.
func (s *Store) Corrections(entity, prop string, validAt time.Time) ([]Correction, error) {
	es := s.entity(entity)
	es.mu.RLock()
	defer es.mu.RUnlock()
	facts := es.props[prop]
	if len(facts) == 0 {
		return nil, ErrNoFacts
	}
	var out []Correction
	for _, f := range facts {
		if contains(f.ValidFrom, f.ValidTo, validAt) {
			out = append(out, Correction{Value: f.Value, TxFrom: f.TxFrom, TxTo: f.TxTo})
		}
	}
	if len(out) == 0 {
		return nil, ErrValidOutOfRange
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TxFrom.Before(out[j].TxFrom) })
	return out, nil
}
