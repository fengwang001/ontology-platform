// Package api is the public entry point of the in-process CEP matcher for
// the pattern "A -> B within T". Depends on cepmatch. Sentinel errors live
// in cepwin; callers use cepwin.ErrXxx with errors.Is.
package api

import (
	"errors"
	"math/rand"
	"reflect"

	"ontology/cepmatch"
	"ontology/cepwin"
)

type (
	Mode  = cepwin.Mode
	Event = cepwin.Event
	Match = cepmatch.Match
)

const (
	Strict  = cepwin.Strict
	Relaxed = cepwin.Relaxed
)

type Matcher struct{ eng *cepmatch.Engine }

// New builds a Matcher: T >= 0, maxPending > 0, mode valid.
func New(mode Mode, T int64, maxPending int) (*Matcher, error) {
	eng, err := cepmatch.New(mode, T, maxPending)
	if err != nil {
		return nil, err
	}
	return &Matcher{eng}, nil
}

// Feed pushes one batch; a rejected batch is a full no-op (invariant 4).
func (m *Matcher) Feed(evs []Event) ([]Match, error) { return m.eng.Feed(evs) }

// Matches returns a snapshot copy of all matches so far; safe with Feed.
func (m *Matcher) Matches() []Match { return m.eng.Matches() }

// SelfCheck verifies the four invariants on fresh engines.
func (m *Matcher) SelfCheck() error {
	var es []error
	bad := func(err error) {
		if err != nil {
			es = append(es, err)
		}
	}
	ev := func(k, ty string, ts int64) Event { return Event{Key: k, Type: ty, TS: ts} }
	pair := func(a, b int64) Match { return Match{A: ev("k", "A", a), B: ev("k", "B", b)} }
	ten := []Event{ev("k", "A", 1), ev("k", "C", 2), ev("k", "B", 3), ev("k", "A", 4), ev("k", "A", 6),
		ev("k", "B", 9), ev("k", "B", 11), ev("k", "A", 12), ev("z", "A", 14), ev("k", "B", 17)}
	for _, c := range []struct {
		mode Mode
		want []Match
	}{{Relaxed, []Match{pair(1, 3), pair(4, 9), pair(6, 11), pair(12, 17)}},
		{Strict, []Match{pair(6, 9), pair(12, 17)}}} {
		e, _ := cepmatch.New(c.mode, 5, 8)
		got, err := e.Feed(ten)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			bad(errors.New("selfcheck: ten-event mismatch"))
		}
	}
	for seed := int64(0); seed < 24; seed++ {
		evs := genEvents(rand.New(rand.NewSource(seed)))
		for _, mode := range []Mode{Strict, Relaxed} {
			e, _ := cepmatch.New(mode, 5, 16)
			if _, err := e.Feed(evs); err != nil {
				bad(err)
				continue
			}
			got := e.Matches()
			if !reflect.DeepEqual(got, naive(mode, 5, evs)) {
				bad(errors.New("selfcheck: naive mismatch"))
			}
			bad(verifyLegal(got, 5, mode == Strict, evs))
		}
	}
	e, _ := cepmatch.New(Relaxed, 5, 4)
	_, err := e.Feed([]Event{ev("k", "A", 1)})
	bad(err)
	snap := e.Matches()
	if _, err := e.Feed([]Event{ev("k", "A", 6), ev("q", "", 7)}); err == nil {
		bad(errors.New("selfcheck: bad batch accepted"))
	}
	if !reflect.DeepEqual(e.Matches(), snap) {
		bad(errors.New("selfcheck: state changed by rejected batch"))
	}
	if _, err := e.Feed([]Event{ev("k", "B", 4)}); err != nil {
		bad(errors.New("selfcheck: unusable after rejection"))
	}
	return errors.Join(es...)
}

func naive(mode Mode, t int64, evs []Event) (out []Match) {
	used, prev := map[Event]bool{}, map[string]Event{}
	win := func(a, b Event) bool { d := b.TS - a.TS; return d >= 0 && d <= t }
	for j, b := range evs {
		a, ok := prev[b.Key]
		switch {
		case mode == Strict && ok && b.Type == "B" && a.Type == "A" && win(a, b):
			out = append(out, Match{A: a, B: b})
		case mode == Relaxed && b.Type == "B":
			for i := 0; i < j; i++ {
				if x := evs[i]; x.Key == b.Key && x.Type == "A" && !used[x] && win(x, b) {
					used[x], out = true, append(out, Match{A: x, B: b})
					break
				}
			}
		}
		prev[b.Key] = b
	}
	return
}

func verifyLegal(ms []Match, t int64, strict bool, evs []Event) error {
	seen, pos := map[Event]bool{}, map[Event]int{}
	for i, x := range evs {
		pos[x] = i
	}
	for _, p := range ms {
		d := p.B.TS - p.A.TS
		bad := p.A.Key != p.B.Key || p.A.Type != "A" || p.B.Type != "B" || d < 0 || d > t
		if strict {
			for _, x := range evs[pos[p.A]+1 : pos[p.B]] {
				bad = bad || x.Key == p.A.Key
			}
		}
		if bad {
			return errors.New("verify: illegal pair")
		}
		if seen[p.A] || seen[p.B] {
			return errors.New("verify: event matched twice")
		}
		seen[p.A], seen[p.B] = true, true
	}
	return nil
}

// genEvents: global TS strictly increasing, so Event uniquely identifies.
func genEvents(r *rand.Rand) (evs []Event) {
	keys, types, ts := []string{"k", "z"}, []string{"A", "B", "C", "A"}, int64(0)
	for range 60 {
		ts += 1 + int64(r.Intn(3))
		evs = append(evs, Event{Key: keys[r.Intn(2)], Type: types[r.Intn(4)], TS: ts})
	}
	return
}
