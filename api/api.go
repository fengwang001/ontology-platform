// Package api is the public facade for count-based sliding-window TopK.
// It depends only on topk (which depends on wtop); the dependency direction
// is one-way. All state lives in process memory and is concurrency-safe.
package api

import (
	"errors"
	"sync"

	"ontology/topk"
)

// Change is one incoming event; Entry is one ranked key.
type Change = topk.Change
type Entry = topk.Entry

// Sentinel errors: callers can discriminate with errors.Is.
var (
	ErrInvalidN = errors.New("api: N must be >= 1")
	ErrInvalidK = errors.New("api: K must satisfy 1 <= K <= N")
	ErrEmptyKey = errors.New("api: change key must not be empty")
)

// Engine maintains the TopK view of the most recent N changes.
type Engine struct {
	mu    sync.RWMutex
	n, k  int
	model *topk.Model
}

// New constructs an engine after validating parameters; no state exists on
// error, so an invalid New cannot leave anything behind.
func New(n, k int) (*Engine, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	if k <= 0 || k > n {
		return nil, ErrInvalidK
	}
	return &Engine{n: n, k: k, model: topk.New(k, n)}, nil
}

// Feed applies a whole batch atomically: every change is validated first, so
// if any key is empty none of the batch touches the window, sums or TopK.
func (e *Engine) Feed(evs []Change) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range evs {
		if c.Key == "" {
			return ErrEmptyKey
		}
	}
	for _, c := range evs {
		e.model.Apply(c)
	}
	return nil
}

// TopK returns up to K entries (sum desc, key asc); safe for concurrent use.
func (e *Engine) TopK() []Entry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.model.TopK()
}

// SelfCheck runs the built-in invariant checks; safe for concurrent use.
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.model.SelfCheck()
}
