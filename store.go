package ontology

import (
	"sync"
	"time"
)

// Store is an in-memory bitemporal fact store. It is safe for concurrent
// use: writes to different entities never block each other, and reads of an
// entity never observe a half-applied write to that entity.
type Store struct {
	mu       sync.Mutex
	entities map[string]*entityState
}

type entityState struct {
	mu     sync.RWMutex
	lastTx time.Time
	props  map[string][]*Fact
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{entities: make(map[string]*entityState)}
}

func (s *Store) entity(name string) *entityState {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, ok := s.entities[name]
	if !ok {
		es = &entityState{props: make(map[string][]*Fact)}
		s.entities[name] = es
	}
	return es
}

func validateWrite(validFrom, validTo, txAt time.Time) error {
	switch {
	case validFrom.IsZero():
		return ErrValidFromZero
	case !validTo.IsZero() && validTo.Equal(validFrom):
		return ErrEmptyInterval
	case !validTo.IsZero() && validTo.Before(validFrom):
		return ErrInvertedInterval
	case txAt.IsZero():
		return ErrTxZero
	}
	return nil
}

// Write records that entity's property held value during
// [validFrom, validTo), known to the system at txAt.
//
// Currently visible facts of the same entity/property whose valid interval
// overlaps the new one are closed on the transaction axis (their TxTo is
// set to txAt), and the parts of their valid interval not covered by the
// new fact survive as residual facts known from txAt. Closed facts are
// never deleted or otherwise modified, so historical AsOf queries are
// repeatable.
//
// txAt must be strictly greater than the entity's last write time,
// otherwise ErrTxRegression is returned and nothing changes.
func (s *Store) Write(entity, prop string, value any, validFrom, validTo, txAt time.Time) error {
	if err := validateWrite(validFrom, validTo, txAt); err != nil {
		return err
	}
	es := s.entity(entity)
	es.mu.Lock()
	defer es.mu.Unlock()
	if !es.lastTx.IsZero() && !txAt.After(es.lastTx) {
		return ErrTxRegression
	}
	facts := es.props[prop]
	next := make([]*Fact, 0, len(facts)+3)
	for _, old := range facts {
		if !old.TxTo.IsZero() || !overlaps(old.ValidFrom, old.ValidTo, validFrom, validTo) {
			next = append(next, old)
			continue
		}
		old.TxTo = txAt
		next = append(next, old)
		if r := residualLeft(entity, prop, old, validFrom); r != nil {
			next = append(next, r)
		}
		if r := residualRight(entity, prop, old, validTo); r != nil {
			next = append(next, r)
		}
	}
	next = append(next, &Fact{
		Entity: entity, Property: prop, Value: value,
		ValidFrom: validFrom, ValidTo: validTo, TxFrom: txAt,
	})
	es.props[prop] = next
	es.lastTx = txAt
	return nil
}

// residualLeft returns the part of old's valid interval strictly before
// newFrom, or nil if there is none. Boundaries are copied exactly.
func residualLeft(entity, prop string, old *Fact, newFrom time.Time) *Fact {
	if !old.ValidFrom.Before(newFrom) {
		return nil
	}
	return &Fact{
		Entity: entity, Property: prop, Value: old.Value,
		ValidFrom: old.ValidFrom, ValidTo: newFrom, TxFrom: old.TxTo,
	}
}

// residualRight returns the part of old's valid interval strictly after
// newTo, or nil if there is none. A zero newTo means +infinity, which
// covers everything to the right.
func residualRight(entity, prop string, old *Fact, newTo time.Time) *Fact {
	if cmpTo(newTo, old.ValidTo) >= 0 {
		return nil
	}
	return &Fact{
		Entity: entity, Property: prop, Value: old.Value,
		ValidFrom: newTo, ValidTo: old.ValidTo, TxFrom: old.TxTo,
	}
}
