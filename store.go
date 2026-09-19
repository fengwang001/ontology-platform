package ontology

import (
	"sort"
	"sync"
	"time"
)

const shardCount = 64

// Record is one bitemporal fact: a Value held during Valid, known by the
// system during Tx. A zero Tx.To means the fact is still current.
type Record struct {
	Valid Interval
	Tx    Interval
	Value string
}

// LookupStatus lets callers distinguish the three "not found" cases.
type LookupStatus int

const (
	// StatusFound is returned when a matching record exists.
	StatusFound LookupStatus = iota
	// StatusNoFacts: the entity/attribute has never had any fact.
	StatusNoFacts
	// StatusOutsideValidity: facts exist, but none cover validAt.
	StatusOutsideValidity
	// StatusNotYetKnown: a fact covers validAt, but at txAt the system
	// did not know it yet (or it had already been superseded).
	StatusNotYetKnown
)

type attrState struct {
	lastWrite time.Time
	attrs     map[string][]*Record
}

// Store is an in-process bitemporal attribute store. All time is supplied
// by callers; the store never reads the wall clock.
type Store struct {
	shards [shardCount]struct {
		mu    sync.Mutex
		store map[string]*attrState
	}
}

// NewStore creates an empty store.
func NewStore() *Store {
	s := &Store{}
	for i := range s.shards {
		s.shards[i].store = make(map[string]*attrState)
	}
	return s
}

func (s *Store) shard(entity string) *struct {
	mu    sync.Mutex
	store map[string]*attrState
} {
	h := uint32(2166136261)
	for i := 0; i < len(entity); i++ {
		h ^= uint32(entity[i])
		h *= 16777619
	}
	return &s.shards[h%shardCount]
}

func (s *Store) stateLocked(sh *struct {
	mu    sync.Mutex
	store map[string]*attrState
}, entity string) *attrState {
	st := sh.store[entity]
	if st == nil {
		st = &attrState{attrs: make(map[string][]*Record)}
		sh.store[entity] = st
	}
	return st
}

func cloneRecords(rs []*Record) []Record {
	out := make([]Record, len(rs))
	for i, r := range rs {
		out[i] = *r
	}
	return out
}

func sortByValidFrom(rs []Record) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Valid.From.Before(rs[j].Valid.From) })
}
