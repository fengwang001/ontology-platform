// Package api is the time-travel store's face; depends on chain, never reverse.
package api

import (
	"errors"
	"fmt"
	"maps"
	"sync"

	"ontology/chain"
	"ontology/ver"
)

// Sentinel errors for rejected operations; each is distinct.
var ErrEmptyKey = errors.New("api: empty key")
var ErrNegativeTS = errors.New("api: negative timestamp")
var ErrEmptyValue = errors.New("api: empty value")

// Store holds one version chain per key, all in process memory.
type Store struct {
	mu     sync.RWMutex
	chains map[string]*chain.Chain
}

func New() *Store { return &Store{chains: map[string]*chain.Chain{}} }

func (s *Store) put(key string, ts int64, v ver.Version) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ts < 0 {
		return ErrNegativeTS
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chains[key]
	if !ok {
		c = chain.New()
		s.chains[key] = c
	}
	c.Insert(v)
	return nil
}

// Write inserts version (ts, value). Rejects empty key, ts<0, empty value.
func (s *Store) Write(key string, ts int64, value string) error {
	if value == "" {
		return ErrEmptyValue
	}
	return s.put(key, ts, ver.Value(ts, value))
}

// Delete inserts a tombstone version at ts. Rejects empty key, ts<0.
func (s *Store) Delete(key string, ts int64) error {
	return s.put(key, ts, ver.Tombstone(ts))
}

// AsOf returns key's value at T: the newest version with ts <= T;
// ok is false on tombstone or when no version is <= T.
func (s *Store) AsOf(key string, T int64) (string, bool) {
	s.mu.RLock()
	c, ok := s.chains[key]
	s.mu.RUnlock()
	if ok {
		if v, found := c.AsOf(T); found {
			return v.Get()
		}
	}
	return "", false
}

// ViewAsOf snapshots every key's visible value at time T.
func (s *Store) ViewAsOf(T int64) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for k, c := range s.chains {
		if v, ok := c.AsOf(T); ok {
			if val, vis := v.Get(); vis {
				out[k] = val
			}
		}
	}
	return out
}

// SelfCheck verifies the four invariants on a fresh internal store.
func (s *Store) SelfCheck() error {
	ops := []op{{"K", "a", 10, false}, {"K", "b", 30, false}, {"K", "c", 20, false},
		{"K", "", 40, true}, {"L", "x", 5, false}, {"L", "y", 50, false}}
	st := New()
	for _, o := range ops {
		if err := o.apply(st); err != nil {
			return err
		}
	}
	// Invariants 2+3: as-of matches naive scan; view matches replay.
	for _, T := range []int64{0, 15, 25, 30, 40, 100} {
		replay := map[string]string{}
		for _, key := range []string{"K", "L", "absent"} {
			wantV, wantOK := naiveAsOf(ops, key, T)
			if gotV, gotOK := st.AsOf(key, T); gotV != wantV || gotOK != wantOK {
				return fmt.Errorf("selfcheck: as-of %s@%d", key, T)
			}
			if wantOK {
				replay[key] = wantV
			}
		}
		if !maps.Equal(st.ViewAsOf(T), replay) {
			return fmt.Errorf("selfcheck: view @%d", T)
		}
	}
	before := st.ViewAsOf(1 << 60)
	for _, err := range []error{
		st.Write("", 1, "v"), st.Write("K", -1, "v"), st.Write("K", 1, ""),
		st.Delete("", 1), st.Delete("K", -1),
	} {
		if err == nil {
			return errors.New("selfcheck: invalid op accepted")
		}
	}
	if !maps.Equal(st.ViewAsOf(1<<60), before) {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}

type op struct {
	key, val string
	ts       int64
	del      bool
}

func (o op) apply(st *Store) error {
	if o.del {
		return st.Delete(o.key, o.ts)
	}
	return st.Write(o.key, o.ts, o.val)
}

// naiveAsOf replays ops: latest visible write for key with ts <= T.
func naiveAsOf(ops []op, key string, T int64) (string, bool) {
	best, val, ok := int64(-1), "", false
	for _, o := range ops {
		if o.key == key && o.ts <= T && o.ts > best {
			best, val, ok = o.ts, o.val, !o.del
		}
	}
	return val, ok
}
