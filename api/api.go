// Package api is the outward face of the consistent key-hash sampler.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"ontology/khash"
	"ontology/samp"
)

// Event is one upstream change.
type Event = samp.Event

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrRateRange   = samp.ErrRateRange
	ErrEmptyKey    = samp.ErrEmptyKey
	ErrTooManyKeys = samp.ErrTooManyKeys
)

// Sampler is safe for concurrent use.
type Sampler struct{ s *samp.Sampler }

// New rejects out-of-range rates and maxKeys < 1 with ErrRateRange.
func New(rate, maxKeys int) (*Sampler, error) {
	s, err := samp.New(rate, maxKeys)
	if err != nil {
		return nil, err
	}
	return &Sampler{s}, nil
}

// Feed returns the sampled events in order; a rejected batch changes nothing.
func (a *Sampler) Feed(evs []Event) ([]Event, error) { return a.s.Feed(evs) }

// SetRate returns the newly included and removed known keys, ordered by (bucket, key).
func (a *Sampler) SetRate(r int) (added, removed []string, err error) { return a.s.SetRate(r) }

// Sampled reports whether key is sampled at the current rate.
func (a *Sampler) Sampled(key string) bool {
	return key != "" && khash.Sampled(key, a.s.Rate())
}

var checkKeys = []string{"gnj", "dzv", "gnk", "kcm", "dzv", "qjy", "a6m", "dzv"}

func naiveFeed(evs []Event, rate int) []Event {
	var out []Event
	for _, e := range evs {
		if khash.Sampled(e.Key, rate) {
			out = append(out, e)
		}
	}
	return out
}

func naiveDiff(known []string, r1, r2 int) (added, removed []string) {
	diff := func(atR2 bool) []string {
		var out []string
		for _, k := range known {
			if khash.Sampled(k, r2) == atR2 && khash.Sampled(k, r1) != atR2 {
				out = append(out, k)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			bi, bj := khash.Bucket(out[i]), khash.Bucket(out[j])
			return bi < bj || bi == bj && out[i] < out[j]
		})
		return out
	}
	return diff(true), diff(false)
}

// SelfCheck verifies the four invariants on built-in event sequences and
// rate changes, plus samp's internal probe-bound proof.
func SelfCheck() error {
	s, err := New(2500, 100)
	if err != nil {
		return err
	}
	evs := make([]Event, len(checkKeys))
	for i, k := range checkKeys {
		evs[i] = Event{Key: k, V: int64(i)}
	}
	// Invariant 1: Feed and SetRate agree with the naive reference.
	got, err := s.Feed(evs)
	if err != nil || !reflect.DeepEqual(got, naiveFeed(evs, 2500)) {
		return fmt.Errorf("api: Feed disagrees with naive reference: %v", err)
	}
	known := []string{"gnj", "dzv", "gnk", "kcm", "qjy", "a6m"}
	r1 := 2500
	for _, r2 := range []int{2000, 5000} {
		added, removed, err := s.SetRate(r2)
		wa, wr := naiveDiff(known, r1, r2)
		if err != nil || !reflect.DeepEqual(added, wa) || !reflect.DeepEqual(removed, wr) {
			return fmt.Errorf("api: SetRate(%d) disagrees with naive reference: %v", r2, err)
		}
		r1 = r2
	}
	// Invariant 2: two instances fed different orders agree on every key.
	a, _ := New(2500, 100)
	b, _ := New(2500, 100)
	rev := append([]Event{}, evs...)
	slices.Reverse(rev)
	_, _ = a.Feed(evs)
	_, _ = b.Feed(rev)
	for _, k := range known {
		if a.Sampled(k) != b.Sampled(k) {
			return fmt.Errorf("api: instances disagree on %q", k)
		}
	}
	// Invariant 3: the sampled key set grows monotonically with the rate.
	for lo := 0; lo <= khash.Buckets; lo += 2500 {
		for hi := lo; hi <= khash.Buckets; hi += 2500 {
			for _, k := range known {
				if khash.Sampled(k, lo) && !khash.Sampled(k, hi) {
					return fmt.Errorf("api: monotonicity broken for %q at %d->%d", k, lo, hi)
				}
			}
		}
	}
	// Invariant 4: rejected operations leave no trace.
	c, _ := New(2500, 3)
	batch := []Event{{Key: "gnj"}, {Key: "dzv"}, {Key: "gnk"}}
	before, err := c.Feed(batch)
	if err != nil {
		return err
	}
	if _, _, err = c.SetRate(-1); !errors.Is(err, ErrRateRange) {
		return errors.New("api: out-of-range rate accepted")
	}
	if _, err = c.Feed([]Event{{Key: ""}}); !errors.Is(err, ErrEmptyKey) {
		return errors.New("api: empty key accepted")
	}
	if _, err = c.Feed([]Event{{Key: "qjy"}}); !errors.Is(err, ErrTooManyKeys) {
		return errors.New("api: maxKeys overflow accepted")
	}
	after, err := c.Feed(batch)
	if err != nil || !reflect.DeepEqual(before, after) {
		return errors.New("api: rejected operation left a trace")
	}
	if added, removed, _ := c.SetRate(2500); added != nil || removed != nil {
		return errors.New("api: rejected SetRate changed the rate")
	}
	return samp.SelfCheck()
}
