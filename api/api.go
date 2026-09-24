// Package api is the outward-facing two-stage pre-aggregator for skewed keys;
// dependencies run one way: api -> glob -> local.
package api

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"

	"ontology/glob"
	"ontology/local"
)

// Change is one upstream event and one pushed change record.
type Change = local.Event

// Four pairwise-distinct, judgeable sentinels; rejected ops fail with no trace.
var (
	ErrInvalidH   = errors.New("api: threshold H must be positive")
	ErrEmptyBatch = local.ErrEmptyBatch
	ErrEmptyKey   = local.ErrEmptyKey
	ErrZeroDelta  = local.ErrZeroDelta
)

// Aggregator is safe for concurrent View/Hot/SelfCheck; Feed/flushes are single-threaded.
type Aggregator struct {
	mu   sync.RWMutex
	buf  *local.Buffer
	view *glob.View
}

// New creates an Aggregator with event-count threshold H (H > 0).
func New(H int64) (*Aggregator, error) {
	if H <= 0 {
		return nil, ErrInvalidH
	}
	return &Aggregator{buf: local.NewBuffer(H), view: glob.NewView()}, nil
}

// Feed applies a batch and returns its pushes. The batch is validated in full
// before any state change, so rejection is whole-batch atomic with no trace.
func (a *Aggregator) Feed(evs []Change) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pushed, err := a.buf.Feed(evs)
	if err != nil {
		return nil, err
	}
	a.view.Apply(pushed)
	return pushed, nil
}

// FlushKey pushes k's buffered delta if present and removes k from hot keys.
func (a *Aggregator) FlushKey(k string) []Change {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.buf.FlushKey(k)
	a.view.Apply(p)
	return p
}

// FlushAll pushes all buffered keys lexicographically and clears the hot set.
func (a *Aggregator) FlushAll() []Change {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.buf.FlushAll()
	a.view.Apply(p)
	return p
}

// View returns the global view, excluding any unflushed local buffer.
func (a *Aggregator) View() map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.view.All()
}

// Hot returns hot keys in lexicographic order.
func (a *Aggregator) Hot() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.buf.Hot()
}

// checkConsistent verifies I3 (view replays from pushed records) and I2
// (view[k]+pending[k] equals the batch sum of every event fed so far).
func (a *Aggregator) checkConsistent(total map[string]int64) error {
	v, pend := a.view.All(), a.buf.PendingAll()
	if !maps.Equal(v, a.view.Replay()) {
		return fmt.Errorf("api: invariant 3 replay mismatch: %v", v)
	}
	for k, want := range total {
		if v[k]+pend[k] != want {
			return fmt.Errorf("api: invariant 2 broken for %q: %d+%d != %d", k, v[k], pend[k], want)
		}
	}
	return nil
}

// SelfCheck runs the eight NOTES.md batches on a fresh H=3 instance, verifying
// invariants 1-4 plus the four distinct, judgeable errors.
func (a *Aggregator) SelfCheck() error {
	g, _ := New(3)
	batches := [][]Change{
		{{Key: "A", Delta: 5}}, {{Key: "A", Delta: 3}}, {{Key: "A", Delta: 2}}, {{Key: "A", Delta: -4}},
		{{Key: "B", Delta: 7}, {Key: "B", Delta: 2}},
		{{Key: "A", Delta: 1}, {Key: "B", Delta: 1}},
		{{Key: "C", Delta: 2}, {Key: "C", Delta: 3}, {Key: "C", Delta: 1}},
		{{Key: "C", Delta: 1}},
	}
	total := map[string]int64{}
	for i, evs := range batches {
		if _, err := g.Feed(evs); err != nil {
			return fmt.Errorf("api: self-check batch %d: %w", i+1, err)
		}
		for _, e := range evs {
			total[e.Key] += e.Delta
		}
		if err := g.checkConsistent(total); err != nil {
			return err
		}
		if i == 4 { // 甲: after batch 5 B is buffered {9,2} and invisible
			if _, ok := g.View()["B"]; ok {
				return errors.New("api: self-check: B visible after batch 5")
			}
		}
	}
	v0, p0, h0 := g.View(), g.buf.PendingAll(), strings.Join(g.Hot(), ",")
	bad := [][]Change{nil, {{Key: "", Delta: 1}}, {{Key: "Z", Delta: 0}}}
	wants := []error{ErrEmptyBatch, ErrEmptyKey, ErrZeroDelta}
	for i, evs := range bad {
		if _, err := g.Feed(evs); !errors.Is(err, wants[i]) ||
			!maps.Equal(g.View(), v0) || !maps.Equal(g.buf.PendingAll(), p0) ||
			strings.Join(g.Hot(), ",") != h0 {
			return fmt.Errorf("api: self-check rejection %v: err=%v or state changed", evs, err)
		}
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidH) {
		return fmt.Errorf("api: self-check New(0): %v", err)
	}
	g.FlushAll() // I1: after FlushAll, view equals the grouped batch sum
	if !maps.Equal(g.View(), map[string]int64{"A": 7, "B": 10, "C": 7}) {
		return fmt.Errorf("api: invariant 1 mismatch: %v", g.View())
	}
	return nil
}
