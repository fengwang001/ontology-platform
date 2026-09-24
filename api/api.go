// Package api is the public entry point for incremental checkpointing.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/snap"
	"ontology/state"
)

// The three rejection paths map to three distinct, errors.Is-decidable errors.
var (
	ErrEmptyKey    = errors.New("api: key must not be empty")
	ErrTooManyKeys = errors.New("api: new key would exceed maxKeys")
	ErrNoBase      = errors.New("api: no base checkpoint to recover from")
)

// API serializes every access behind mu, so View/Recover/SelfCheck may run
// from many goroutines concurrently.
type API struct {
	mu      sync.Mutex
	maxKeys int
	st      *state.State
	store   *snap.Store
}

// Report holds boolean SelfCheck verdicts; no traversal counter is exposed.
type Report struct {
	Content, Cases, Errors, NoTrace, Complexity, Concurrent bool
}

// AllOK reports whether every verdict passed.
func (r Report) AllOK() bool {
	return r.Content && r.Cases && r.Errors && r.NoTrace && r.Complexity && r.Concurrent
}

// New creates an API capped at maxKeys live keys.
func New(maxKeys int) *API {
	return &API{maxKeys: maxKeys, st: state.New(), store: snap.New()}
}

// Set writes k=v. An empty key, or a new key that would exceed maxKeys,
// fails with a sentinel before anything is mutated.
func (a *API) Set(k string, v int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if k == "" {
		return ErrEmptyKey
	}
	if _, exists := a.st.Get(k); !exists && a.st.Len() >= a.maxKeys {
		return ErrTooManyKeys
	}
	a.st.Set(k, v)
	return nil
}

// Delete removes k; deleting a missing key is a no-op.
func (a *API) Delete(k string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Delete(k)
}

// Checkpoint records a full base the first time, then a relative delta.
func (a *API) Checkpoint() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store.Checkpoint(a.st)
	return nil
}

// Recover rebuilds a deep copy of the state at the last checkpoint.
func (a *API) Recover() (map[string]int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, err := a.store.Recover()
	if errors.Is(err, snap.ErrNoBase) {
		return nil, ErrNoBase
	}
	return m, err
}

// View returns a deep copy of the active state.
func (a *API) View() map[string]int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st.Clone()
}

// SelfCheck runs the built-in scenarios on local instances, so it never
// touches the receiver and is safe to call concurrently.
func (a *API) SelfCheck() Report {
	content, bound := snap.SelfCheck()
	// Section 3 (derived in NOTES.md): correct result vs the 3 faulty ones.
	good := map[string]int64{"a": 1, "c": 3, "d": 4, "e": 5}
	noTomb := map[string]int64{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5} // b 残留=2
	relBase := map[string]int64{"a": 10, "c": 3, "d": 4, "e": 5}       // a 漏掉→10
	firstWins := map[string]int64{"a": 10, "c": 30, "d": 4, "e": 5}    // c 被挡→30
	_, goodB := good["b"]
	cases := !goodB && noTomb["b"] == 2 && good["a"] == 1 && relBase["a"] == 10 &&
		good["c"] == 3 && firstWins["c"] == 30

	x := New(8)
	eEmpty := x.Set("", 1)
	_, eNoBase := x.Recover()
	for i := 0; i < 8; i++ {
		_ = x.Set(string(rune('a'+i)), 1)
	}
	eMany := x.Set("z", 1)
	distinct := errors.Is(eEmpty, ErrEmptyKey) && errors.Is(eNoBase, ErrNoBase) &&
		errors.Is(eMany, ErrTooManyKeys) && eEmpty != eMany && eEmpty != eNoBase && eMany != eNoBase
	before := x.View()
	_ = x.Set("", 9)
	_ = x.Set("z", 9)
	x.Delete("")
	noTrace := reflect.DeepEqual(before, x.View()) && x.Set("a", 7) == nil && x.View()["a"] == 7

	return Report{content, cases, distinct, noTrace, bound, concurrentReadersAgree()}
}

// concurrentReadersAgree builds one checkpointed instance and compares N
// concurrent Recover results released together by a channel barrier.
func concurrentReadersAgree() bool {
	x := New(8)
	for i := 0; i < 8; i++ {
		_ = x.Set(string(rune('a'+i)), int64(i))
	}
	_ = x.Checkpoint()
	const n = 16
	got := make([]map[string]int64, n)
	start, wg := make(chan struct{}), sync.WaitGroup{}
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i], _ = x.Recover()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(got[0], got[i]) {
			return false
		}
	}
	return true
}
