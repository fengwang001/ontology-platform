// Package api is the late-correction reprocessing engine; dependencies
// point api -> agg and api -> chlog only.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/agg"
	"ontology/chlog"
)

var (
	ErrInvalidW   = errors.New("api: W must be > 0")
	ErrEmptyKey   = errors.New("api: key must not be empty")
	ErrInvalidSeq = errors.New("api: seq must be > 0")
)

type Change = chlog.Change

type Status uint8

// Status values share agg.Kind's 1..5 order; keep them aligned.
const (
	StatusNew, StatusLate, StatusCorrection, StatusDuplicate, StatusStale Status = 1, 2, 3, 4, 5
)

// API is the in-process engine; use New, the zero value is not usable.
type API struct {
	mu   sync.Mutex
	w    int
	keys map[string]*agg.Aggregator
	log  *chlog.Log
}

// New creates an engine with per-key correction windows of W members.
func New(W int) (*API, error) {
	if W <= 0 {
		return nil, ErrInvalidW
	}
	return &API{w: W, keys: map[string]*agg.Aggregator{}, log: chlog.New()}, nil
}

// Apply processes one event and returns the changes it emitted. Empty key
// and seq<=0 fail before any state is created; an expired correction is
// StatusStale, a legal counted rejection (nil error).
func (x *API) Apply(key string, seq, val int64) ([]Change, Status, error) {
	if key == "" {
		return nil, 0, ErrEmptyKey
	}
	if seq <= 0 {
		return nil, 0, ErrInvalidSeq
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	a := x.keys[key]
	if a == nil {
		a = agg.New(x.w)
		x.keys[key] = a
	}
	o := a.Apply(seq, val)
	st := Status(o.Kind)
	if st == StatusStale || st == StatusDuplicate || (o.Had && !o.Changed) {
		return nil, st, nil
	}
	p := Change{Plus: true, Key: key, Sum: o.NewSum}
	if !o.Had {
		return []Change{p}, st, x.log.Append(p)
	}
	m := Change{Plus: false, Key: key, Sum: o.OldSum}
	if err := x.log.Append(m); err != nil {
		return nil, 0, err
	}
	return []Change{m, p}, st, x.log.Append(p)
}

// View returns a snapshot sum for every key that has appeared.
func (x *API) View() map[string]int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make(map[string]int64, len(x.keys))
	for k, a := range x.keys { // lock order is always api -> agg
		out[k] = a.Sum()
	}
	return out
}

// Stale returns the total number of rejected expired corrections.
func (x *API) Stale() int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	var n int64
	for _, a := range x.keys {
		n += a.Stale()
	}
	return n
}

// SelfCheck replays the built-in eight-event sequence and verifies all
// four invariants; nil means they all hold.
func (x *API) SelfCheck() error {
	const K = "k" // the eight prescribed events, W=3
	seq := []int64{5, 7, 6, 8, 5, 6, 6, 9}
	val := []int64{10, 20, 15, 5, 99, 15, 30, 3}
	st := []Status{StatusNew, StatusNew, StatusLate, StatusNew,
		StatusStale, StatusDuplicate, StatusCorrection, StatusNew}
	sum := []int64{10, 30, 45, 50, 50, 50, 65, 68}
	nch := []int{1, 2, 2, 2, 0, 0, 2, 2}
	e, _ := New(3)
	for i := range seq { // statuses/sums/counts; then invariant 3
		cs, s, err := e.Apply(K, seq[i], val[i])
		if err != nil || s != st[i] || e.View()[K] != sum[i] || len(cs) != nch[i] {
			return fmt.Errorf("step %d: %v", i+1, err)
		}
		v0, z0, l0 := e.View()[K], e.Stale(), e.log.Len()
		_, r, _ := e.Apply(K, seq[i], val[i]) // identical redelivery
		if e.View()[K] != v0 || e.log.Len() != l0 ||
			(r == StatusStale) != (e.Stale() == z0+1) {
			return fmt.Errorf("step %d redelivery trace", i+1)
		}
	}
	chs := e.log.Changes() // invariant 2: every prefix replies consistently
	for n := range len(chs) + 1 {
		if _, err := chlog.ReplayPrefix(chs, n); err != nil {
			return err
		}
	}
	// Invariant 1: naive batch sum of final Vals per distinct Seq
	// ((5,99) rejected): 10+20+30+5+3 = 68.
	if e.View()[K] != 68 || e.Stale() != 2 {
		return fmt.Errorf("invariant 1: view=%d stale=%d", e.View()[K], e.Stale())
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidW) { // invariant 4
		return err
	}
	g, _ := New(2)
	g.Apply("a", 1, 1)
	v0, z0, l0 := g.View(), g.Stale(), g.log.Len()
	_, _, e1 := g.Apply("", 1, 1)
	_, _, e2 := g.Apply("a", 0, 1)
	if !errors.Is(e1, ErrEmptyKey) || !errors.Is(e2, ErrInvalidSeq) ||
		!reflect.DeepEqual(g.View(), v0) || g.Stale() != z0 || g.log.Len() != l0 {
		return errors.New("hard rejection invalid or left a trace")
	}
	_, _, err := g.Apply("c", 2, 2) // engine stays usable
	return err
}
