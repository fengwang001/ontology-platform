// Package api is the concurrency-safe face of the retractable top-K
// maintainer: validate-before-mutate and a RWMutex for concurrent readers.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/ord"
	"ontology/topk"
)

// Distinct, decidable sentinel errors; match them with errors.Is.
var (
	ErrInvalidParams = errors.New("api: k must be >= 1 and cap >= k")
	ErrEmptyID       = errors.New("api: id must not be empty")
	ErrCapacity      = errors.New("api: capacity reached for new id")
)

// TopK is a concurrency-safe retractable top-K maintainer.
type TopK struct {
	k, capN int
	mu      sync.RWMutex
	m       *topk.Maintainer
}

// New builds a maintainer, failing before any allocation on bad params.
func New(k, capN int) (*TopK, error) {
	if k <= 0 || capN < k {
		return nil, ErrInvalidParams
	}
	return &TopK{k: k, capN: capN, m: topk.New(k, capN)}, nil
}

// Add upserts id with score; an empty id or a new id while full is rejected
// without changing state.
func (t *TopK) Add(id string, score int64) error {
	if id == "" {
		return ErrEmptyID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.m.Has(id) && t.m.Count() >= t.capN {
		return ErrCapacity
	}
	t.m.Add(ord.Elem{ID: id, Score: score})
	return nil
}

// Remove deletes id; a missing id is an idempotent no-op. Empty id errors.
func (t *TopK) Remove(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m.Remove(id)
	return nil
}

// TopK returns at most k elements (fewer if not enough), best first.
func (t *TopK) TopK() []ord.Elem { t.mu.RLock(); defer t.mu.RUnlock(); return t.m.Tops() }

// Count reports the number of stored elements.
func (t *TopK) Count() int { t.mu.RLock(); defer t.mu.RUnlock(); return t.m.Count() }

// SelfCheck replays a built-in sequence and verifies the four invariants.
func (t *TopK) SelfCheck() error { return selfCheck() }

// ops7 is the section-3 sequence (K=3); want7 holds each step's top ids.
var ops7 = []struct {
	id  string
	sc  int64
	rem bool
}{
	{"m", 10, false}, {"a", 10, false}, {"z", 20, false}, {"y", 20, false},
	{"b", 5, false}, {"z", 0, true}, {"k", 10, false},
}
var want7 = [][]string{
	{"m"}, {"a", "m"}, {"z", "a", "m"}, {"y", "z", "a"},
	{"y", "z", "a"}, {"y", "a", "m"}, {"y", "a", "k"},
}

func naive(live map[string]int64, k int) []ord.Elem {
	all := make([]ord.Elem, 0, len(live))
	for id, sc := range live {
		all = append(all, ord.Elem{ID: id, Score: sc})
	}
	sort.Slice(all, func(i, j int) bool { return ord.Less(all[i], all[j]) })
	return all[:min(len(all), k)]
}
func selfCheck() error {
	t, _ := New(3, 100)
	live := map[string]int64{}
	for i, o := range ops7 {
		var err error
		if o.rem {
			err = t.Remove(o.id)
			delete(live, o.id)
		} else {
			err = t.Add(o.id, o.sc)
			live[o.id] = o.sc
		}
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(t.TopK(), naive(live, 3)) {
			return fmt.Errorf("step %d diverges from naive reference", i+1)
		}
	}
	full := naive(live, len(live))
	for kp := 1; kp <= 3; kp++ {
		tp, _ := New(kp, 100)
		for id, sc := range live {
			if err := tp.Add(id, sc); err != nil {
				return err
			}
		}
		if !reflect.DeepEqual(tp.TopK(), full[:kp]) {
			return fmt.Errorf("prefix k=%d diverges", kp)
		}
	}
	return checkRejections()
}
func checkRejections() error {
	t, _ := New(1, 1)
	_ = t.Add("a", 1)
	for _, c := range []struct{ got, want error }{
		{newErr(0, 2), ErrInvalidParams},
		{newErr(2, 1), ErrInvalidParams},
		{t.Add("", 1), ErrEmptyID},
		{t.Remove(""), ErrEmptyID},
		{t.Add("b", 2), ErrCapacity},
	} {
		if !errors.Is(c.got, c.want) {
			return fmt.Errorf("got %v want %w", c.got, c.want)
		}
	}
	if t.Count() != 1 || t.TopK()[0].ID != "a" {
		return errors.New("a rejected operation left a trace")
	}
	if err := t.Remove("ghost"); err != nil || t.Count() != 1 {
		return errors.New("Remove of missing id is not idempotent")
	}
	return topk.ComplexityCheck()
}
func newErr(k, c int) error { _, e := New(k, c); return e }
