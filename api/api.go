// Package api is the public facade for micro-batch window counting.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/mbatch"
	"ontology/win"
)

// Decidable, mutually distinct sentinel errors.
var (
	ErrNonPositiveWindow = errors.New("api: W must be positive")
	ErrNonPositiveBatch  = errors.New("api: B must be positive")
	ErrEmptyKey          = errors.New("api: event key must not be empty")
)

type (
	Event  = mbatch.Event  // upstream event
	Change = mbatch.Change // changelog entry, Op '+' or '-'
	Window = win.Window    // left-closed right-open interval
)

// Engine counts events per (Key, Window) via micro-batch triggers.
type Engine struct {
	mu  sync.RWMutex
	eng *mbatch.Engine
}

// New validates W and B before building any state.
func New(w, b int64) (*Engine, error) {
	if w <= 0 {
		return nil, ErrNonPositiveWindow
	}
	if b <= 0 {
		return nil, ErrNonPositiveBatch
	}
	return &Engine{eng: mbatch.New(w, b)}, nil
}

// Feed rejects the whole batch if any event is invalid, leaving all state
// untouched; otherwise appends events and triggers full micro-batches.
func (e *Engine) Feed(evs []Event) ([]Change, error) {
	for _, ev := range evs {
		if ev.Key == "" {
			return nil, ErrEmptyKey
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.eng.Feed(evs), nil
}

// Flush triggers the tail batch with wm = +inf, closing all windows.
func (e *Engine) Flush() []Change {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.eng.Flush()
}

// Emitted returns a copy of the full changelog.
func (e *Engine) Emitted() []Change {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.eng.Emitted()
}

// View applies the whole changelog: current value per (Key, Window).
func (e *Engine) View() map[string]map[Window]int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v := map[string]map[Window]int64{}
	for _, c := range e.eng.Emitted() {
		if c.Op == '+' {
			if v[c.Key] == nil {
				v[c.Key] = map[Window]int64{}
			}
			v[c.Key][c.Win] = c.Count
		} else {
			delete(v[c.Key], c.Win)
		}
	}
	return v
}

// SelfCheck verifies the four invariants on built-in sequences. It only
// builds fresh engines, so it is safe for concurrent use.
func (e *Engine) SelfCheck() error {
	eng, _ := New(10, 3)
	var evs []Event
	for _, ts := range []int64{5, 12, 20, 7, 22, 25, 8, 31, 15, 9} {
		evs = append(evs, Event{Key: "K", TS: ts})
	}
	if _, err := eng.Feed(evs); err != nil {
		return err
	}
	eng.Flush()
	want := map[Window]int64{}
	for _, ev := range evs {
		want[win.Of(win.Index(ev.TS, 10), 10)]++
	}
	if !reflect.DeepEqual(eng.View(), map[string]map[Window]int64{"K": want}) {
		return errors.New("api selfcheck: view != batch recompute")
	}
	type pair struct {
		k string
		w Window
	}
	cur, last := map[pair]int64{}, map[pair]byte{}
	for _, c := range eng.Emitted() {
		p := pair{c.Key, c.Win}
		switch c.Op {
		case '+':
			if last[p] != 0 && last[p] != '-' {
				return errors.New("api selfcheck: window closed more than once")
			}
			cur[p] = c.Count
		case '-':
			if n, ok := cur[p]; last[p] != '+' || !ok || n != c.Count {
				return errors.New("api selfcheck: bad retraction in changelog")
			}
			delete(cur, p)
		}
		last[p] = c.Op
	}
	if _, err := New(0, 1); !errors.Is(err, ErrNonPositiveWindow) {
		return errors.New("api selfcheck: rejected op left a trace")
	}
	if _, err := New(1, 0); !errors.Is(err, ErrNonPositiveBatch) {
		return errors.New("api selfcheck: rejected op left a trace")
	}
	fresh, _ := New(10, 3)
	if _, err := fresh.Feed([]Event{{Key: "K", TS: 5}, {Key: "", TS: 7}}); !errors.Is(err, ErrEmptyKey) {
		return errors.New("api selfcheck: rejected op left a trace")
	}
	got, _ := fresh.Feed([]Event{{Key: "K", TS: 1}, {Key: "K", TS: 2}})
	if len(got) != 0 || len(fresh.Emitted()) != 0 || len(fresh.View()) != 0 {
		return errors.New("api selfcheck: rejected feed left a trace")
	}
	if got, _ = fresh.Feed([]Event{{Key: "K", TS: 12}}); len(got) == 0 {
		return errors.New("api selfcheck: engine unusable after rejection")
	}
	return nil
}
