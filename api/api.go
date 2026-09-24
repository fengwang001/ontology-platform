// Package api is the public face of stream compaction: in-memory multiset state, replay and self checks.
package api

import "errors"
import "fmt"
import "maps"
import "math/rand/v2"
import "slices"
import "ontology/ev"
import "ontology/fold"

var (
	ErrStreamTooLong  = errors.New("api: stream longer than maxLen") // len(stream) > maxLen
	ErrIllegalRetract = errors.New("api: retract of absent value")   // Retract with no live copy
)

// State is, per group Key, the live multiplicity of each value.
type State map[string]map[int64]int

// API is safe for concurrent use.
type API struct {
	maxLen int
	f      *fold.Compacter
}

// New creates an API rejecting streams longer than maxLen; <=0 means no limit.
func New(maxLen int) *API { return &API{maxLen: maxLen, f: fold.New()} }

func (a *API) Compact(stream []ev.Event) ([]ev.Event, error) {
	if a.maxLen > 0 && len(stream) > a.maxLen {
		return nil, fmt.Errorf("%w: %d > %d", ErrStreamTooLong, len(stream), a.maxLen)
	}
	return a.f.Compact(stream)
}

// Replay applies stream to a deep copy of init. It fails outright on an
// over-long stream, invalid event or illegal retract; init is never mutated.
func (a *API) Replay(init State, stream []ev.Event) (State, error) {
	if a.maxLen > 0 && len(stream) > a.maxLen {
		return nil, fmt.Errorf("%w: %d > %d", ErrStreamTooLong, len(stream), a.maxLen)
	}
	st := make(State, len(init))
	for k, ms := range init {
		st[k] = maps.Clone(ms) // deep copy: failures leave no trace
	}
	for i, e := range stream {
		if err := e.Validate(); err != nil {
			return nil, fmt.Errorf("api: event %d: %w", i, err)
		}
		ms := st[e.Key]
		if ms == nil {
			ms = map[int64]int{}
			st[e.Key] = ms
		}
		if e.Op == ev.Insert {
			ms[e.Val]++
			continue
		}
		if ms[e.Val] <= 0 {
			return nil, fmt.Errorf("%w: event %d key=%q val=%d", ErrIllegalRetract, i, e.Key, e.Val)
		}
		ms[e.Val]-- // drop zero entries: keep state canonical
		if ms[e.Val] == 0 {
			delete(ms, e.Val)
		}
		if len(ms) == 0 {
			delete(st, e.Key)
		}
	}
	return st, nil
}

// SelfCheck exercises the four invariants on built-in streams.
func (a *API) SelfCheck() error {
	I, R := ev.Insert, ev.Retract
	six := []ev.Event{{Key: "g", Val: 7, Op: I}, {Key: "g", Val: 7, Op: I}, {Key: "g", Val: 7, Op: R}, {Key: "g", Val: 7, Op: R}, {Key: "g", Val: 7, Op: I}, {Key: "g", Val: 3, Op: I}}
	if got, err := a.f.Compact(six); err != nil || !slices.Equal(got, []ev.Event{{Key: "g", Val: 7, Op: I}, {Key: "g", Val: 3, Op: I}}) {
		return fmt.Errorf("six-stream compaction: %v %v", got, err)
	}
	rng := rand.New(rand.NewPCG(7, 11))
	for iter := 0; iter < 200; iter++ {
		init := State{"g": {0: 50, 1: 50, 2: 50, 3: 50}} // thick: every generated stream is legal all the way
		s := make([]ev.Event, rng.IntN(40))
		for i := range s {
			s[i] = ev.Event{Key: "g", Val: int64(rng.IntN(4)), Op: ev.Op(1 + rng.IntN(2))}
		}
		c, err := a.Compact(s)
		if err != nil {
			return err
		}
		c2, _ := a.Compact(c)
		end, err1 := a.Replay(init, s)
		end2, err2 := a.Replay(init, c)
		if len(c) > len(s) || !slices.Equal(c, c2) || (err1 == nil) != (err2 == nil) ||
			(err1 == nil && fmt.Sprint(end) != fmt.Sprint(end2)) {
			return fmt.Errorf("iter %d: %v %v vs %v %v", iter, end, err1, end2, err2)
		}
	}
	return nil
}
