// Package samp keeps the current sampling rate and a (bucket, key)-ordered
// index of known keys: Feed and SetRate on top of khash.
package samp

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"sync"

	"ontology/khash"
)

// Event is one upstream change.
type Event struct {
	Key string
	V   int64
}

// Sentinel errors; distinguishable with errors.Is.
var (
	ErrRateRange   = errors.New("samp: rate outside [0,10000] or maxKeys < 1")
	ErrEmptyKey    = errors.New("samp: event key is empty")
	ErrTooManyKeys = errors.New("samp: known keys would exceed maxKeys")
)

type entry struct {
	bucket int
	key    string
}

// Sampler is safe for concurrent use.
type Sampler struct {
	mu      sync.RWMutex
	rate    int
	maxKeys int
	known   map[string]int // key -> bucket
	sorted  []entry        // known keys ordered by (bucket, key)
	checked int            // known keys examined by the last SetRate
}

// New returns a Sampler, rejecting out-of-range rate or maxKeys < 1.
func New(rate, maxKeys int) (*Sampler, error) {
	if rate < 0 || rate > khash.Buckets || maxKeys < 1 {
		return nil, ErrRateRange
	}
	return &Sampler{rate: rate, maxKeys: maxKeys, known: map[string]int{}}, nil
}

// Feed validates the whole batch before touching state; a rejected batch
// changes nothing. Returns the sampled events in their original order.
func (s *Sampler) Feed(evs []Event) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := 0
	for _, e := range evs {
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
		if _, ok := s.known[e.Key]; !ok {
			fresh++
		}
	}
	if len(s.known)+fresh > s.maxKeys {
		return nil, ErrTooManyKeys
	}
	var out []Event
	for _, e := range evs {
		b := khash.Bucket(e.Key)
		if _, ok := s.known[e.Key]; !ok {
			s.known[e.Key] = b
			s.insert(entry{b, e.Key})
		}
		if b < s.rate {
			out = append(out, e)
		}
	}
	return out, nil
}

// SetRate moves the rate to r2 and returns the newly included / removed
// known keys, each ordered by (bucket, key). It binary-searches the bucket
// interval [min(r1,r2), max(r1,r2)) instead of re-judging every known key.
func (s *Sampler) SetRate(r2 int) (added, removed []string, err error) {
	if r2 < 0 || r2 > khash.Buckets {
		return nil, nil, ErrRateRange
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r1 := s.rate
	s.rate = r2
	s.checked = 0
	if r2 == r1 {
		return nil, nil, nil
	}
	lo, hi := min(r1, r2), max(r1, r2)
	from := sort.Search(len(s.sorted), func(i int) bool { s.checked++; return s.sorted[i].bucket >= lo })
	to := sort.Search(len(s.sorted), func(i int) bool { s.checked++; return s.sorted[i].bucket >= hi })
	for _, e := range s.sorted[from:to] {
		s.checked++
		if r2 > r1 {
			added = append(added, e.key)
		} else {
			removed = append(removed, e.key)
		}
	}
	return added, removed, nil
}

// Rate returns the current sampling rate.
func (s *Sampler) Rate() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rate
}

func (s *Sampler) insert(e entry) {
	i := sort.Search(len(s.sorted), func(i int) bool {
		x := s.sorted[i]
		return x.bucket > e.bucket || x.bucket == e.bucket && x.key >= e.key
	})
	s.sorted = append(s.sorted, entry{})
	copy(s.sorted[i+1:], s.sorted[i:])
	s.sorted[i] = e
}

// SelfCheck proves internally that SetRate's probe count stays within
// 2*ceil(log2(m+1)) + 4 + (returned keys) for several m. The counter itself
// never leaves the package; only the pass/fail verdict is exported.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		s, _ := New(5000, m)
		evs := make([]Event, m)
		for i := range evs {
			evs[i] = Event{Key: fmt.Sprintf("k%08x", uint32(i)*2654435761)}
		}
		if _, err := s.Feed(evs); err != nil {
			return err
		}
		for _, r2 := range []int{5001, 5000} {
			added, removed, _ := s.SetRate(r2)
			if bound := 2*bits.Len(uint(m)) + 4 + len(added) + len(removed); s.checked > bound {
				return fmt.Errorf("samp: m=%d checked=%d exceeds bound %d", m, s.checked, bound)
			}
		}
	}
	return nil
}
