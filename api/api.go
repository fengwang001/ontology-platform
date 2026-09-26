// Package api is the public front of the Count-Min Sketch. It owns one
// in-memory sketch and turns every invalid input into a distinct sentinel
// error. Dependency direction: api -> sketch -> ch, never reversed.
package api

import (
	"errors"
	"fmt"

	"ontology/sketch"
)

// The three failure kinds are mutually distinct sentinel errors.
var (
	ErrInvalidParams = errors.New("api: width and depth must be positive")
	ErrInvalidCount  = errors.New("api: count must be positive")
	ErrInvalidKey    = errors.New("api: key must be non-negative")
)

// API is the safe handle handed to callers; all state lives in the sketch.
type API struct {
	s *sketch.Sketch
}

// New constructs a sketch of width w and depth d.
func New(w, d int) (*API, error) {
	if w <= 0 || d <= 0 {
		return nil, ErrInvalidParams
	}
	s, err := sketch.New(w, d)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	return &API{s: s}, nil
}

// Add accumulates c for key k; it rejects before delegating, so a rejected
// call never reaches the table and leaves every counter unchanged.
func (a *API) Add(k, c int64) error {
	if k < 0 {
		return ErrInvalidKey
	}
	if c <= 0 {
		return ErrInvalidCount
	}
	return a.s.Add(k, c)
}

// Query returns the never-underestimating estimate for key k.
func (a *API) Query(k int64) (int64, error) {
	if k < 0 {
		return 0, ErrInvalidKey
	}
	return a.s.Query(k)
}

// SelfCheck runs the sketch invariants and additionally verifies the API
// contract: the three sentinels are distinct, each rejection happens before
// any state change, and the sketch stays usable afterwards. It is read-only
// apart from throwaway sketches, so it is safe under concurrent calls.
func (a *API) SelfCheck() error {
	if err := a.s.SelfCheck(); err != nil {
		return err
	}
	if errors.Is(ErrInvalidParams, ErrInvalidCount) || errors.Is(ErrInvalidCount, ErrInvalidKey) ||
		errors.Is(ErrInvalidParams, ErrInvalidKey) {
		return errors.New("api: sentinel errors must be distinct")
	}
	if _, err := New(0, 3); !errors.Is(err, ErrInvalidParams) {
		return fmt.Errorf("api: New(0,3) err=%v", err)
	}
	tmp, err := New(6, 3)
	if err != nil {
		return err
	}
	if err := tmp.Add(2, 4); err != nil {
		return err
	}
	probe := func() [3]int64 {
		var q [3]int64
		for i, k := range []int64{2, 5, 7} {
			v, err := tmp.Query(k)
			if err != nil {
				panic(err)
			}
			q[i] = v
		}
		return q
	}
	before := probe()
	for _, e := range [][2]int64{{-1, 1}, {1, 0}, {1, -9}} {
		if err := tmp.Add(e[0], e[1]); err == nil {
			return fmt.Errorf("api: Add(%d,%d) unexpectedly accepted", e[0], e[1])
		}
	}
	if got := probe(); got != before {
		return fmt.Errorf("api: rejected calls changed state: %v -> %v", before, got)
	}
	if err := tmp.Add(5, 2); err != nil {
		return err
	}
	if v, _ := tmp.Query(5); v != 2 {
		return fmt.Errorf("api: sketch unusable after rejections: Query(5)=%d", v)
	}
	return nil
}
