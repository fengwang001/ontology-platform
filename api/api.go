// Package api is the external entry point of hinted handoff: it constructs a
// fixed replica set and exposes Write / Up / Down / Get / SelfCheck.
// Dependency direction is one-way: api -> hh -> hint.
package api

import (
	"errors"
	"strconv"

	"ontology/hh"
	"ontology/hint"
)

// Four distinct, decidable failure classes (errors.Is matches across layers).
var (
	ErrBadConfig    = hh.ErrBadConfig
	ErrBadVersion   = hh.ErrBadVersion
	ErrEmptyKey     = hh.ErrEmptyKey
	ErrHintOverflow = hh.ErrHintOverflow
)

// Entry is one replica's current value/version snapshot for a key.
type Entry struct {
	Value string
	Ver   int64
}

// System is the hinted-handoff service; all methods are concurrency-safe.
type System struct{ m *hh.Manager }

// New creates n initially-online replicas with a per-replica hint capacity.
func New(n, maxHints int) (*System, error) {
	m, err := hh.New(n, maxHints)
	if err != nil {
		return nil, err
	}
	return &System{m: m}, nil
}

// Write applies a strict-version (> current) write to online replicas and
// appends an unconditional hint per down replica; a full down buffer rejects
// the whole write atomically with ErrHintOverflow.
func (s *System) Write(key, value string, ver int64) error { return s.m.Write(key, value, ver) }

// Down marks replica r down.
func (s *System) Down(r int) error { return s.m.Down(r) }

// Up recovers replica r, replays its hints in append order (strict >) and
// returns the applied and skipped counts.
func (s *System) Up(r int) (applied, skipped int, err error) { return s.m.Up(r) }

// Get returns every replica's current entry snapshot for key.
func (s *System) Get(key string) []Entry {
	g := s.m.Get(key)
	out := make([]Entry, len(g))
	for i, e := range g {
		out[i] = Entry(e)
	}
	return out
}

// Hints returns a copy of replica r's buffered hints in append order. It is
// an inspection aid for tests/demo; the package-hint scan counter is never
// exposed through any exported method.
func (s *System) Hints(r int) []hint.Entry { return s.m.Hints(r) }

// SelfCheck runs the built-in eight-step scenario on a fresh 3-replica system
// (maxHints 3), then verifies tie replay, replay idempotence, the four
// distinct sentinel errors and no-trace rejection. It ignores receiver state
// and returns the first violation.
func (s *System) SelfCheck() error {
	c, _ := New(3, 3)
	E := func(v string, n int64) Entry { return Entry{Value: v, Ver: n} }
	var bad string
	chk := func(ok bool, msg string) {
		if !ok && bad == "" {
			bad = msg
		}
	}
	_ = c.Down(1)
	// Steps 1-3: online side converges by strict >; down side always hints.
	early := []struct {
		v  string
		n  int64
		on Entry
		nh int
	}{{"a", 5, E("a", 5), 1}, {"b", 7, E("b", 7), 2}, {"c", 6, E("b", 7), 3}}
	for i, w := range early {
		chk(c.Write("k", w.v, w.n) == nil, "step write")
		p := c.Get("k")
		chk(p[0] == w.on && p[2] == w.on && p[1] == (Entry{}) && len(c.Hints(1)) == w.nh,
			"step"+strconv.Itoa(i+1))
	}
	// Step 4: buffer full -> whole write fails atomically; online untouched.
	err := c.Write("k", "d", 8)
	p := c.Get("k")
	chk(errors.Is(err, ErrHintOverflow) && p[0] == E("b", 7) && p[2] == E("b", 7) &&
		len(c.Hints(1)) == 3, "step4")
	// Step 5: replay a5,b7 applies; late c6 is stale -> skipped; all b7.
	a, sk, _ := c.Up(1)
	p = c.Get("k")
	chk(a == 2 && sk == 1 && p[0] == E("b", 7) && p[1] == E("b", 7) && p[2] == E("b", 7) &&
		len(c.Hints(1)) == 0, "step5")
	// Steps 6-7: down again; e9 applies; f9 ties online (skipped) but hinted.
	_ = c.Down(1)
	chk(c.Write("k", "e", 9) == nil && c.Write("k", "f", 9) == nil, "step67w")
	p = c.Get("k")
	hs := c.Hints(1)
	chk(p[0] == E("e", 9) && p[2] == E("e", 9) && p[1] == E("b", 7) && len(hs) == 2 &&
		hs[0].Value == "e" && hs[1].Value == "f", "step7")
	// Step 8: e9 applies, f9 tied skipped -> all e9; a second Up applies 0.
	a, sk, _ = c.Up(1)
	p = c.Get("k")
	chk(a == 1 && sk == 1 && p[0] == E("e", 9) && p[1] == E("e", 9) && p[2] == E("e", 9),
		"step8")
	a2, sk2, _ := c.Up(1)
	chk(a2 == 0 && sk2 == 0, "idempotent")
	// Four distinct errors; the rejected system stays usable.
	_, e1 := New(0, 1)
	_, e2 := New(1, 0)
	chk(errors.Is(e1, ErrBadConfig) && errors.Is(e2, ErrBadConfig), "badconfig")
	d, _ := New(2, 2)
	chk(errors.Is(d.Write("k", "v", 0), ErrBadVersion), "badversion")
	chk(errors.Is(d.Write("", "v", 1), ErrEmptyKey), "emptykey")
	_, _, eu := d.Up(9)
	chk(errors.Is(eu, ErrBadConfig) && errors.Is(d.Down(-1), ErrBadConfig), "range")
	_ = d.Down(0)
	_ = d.Write("k", "v1", 1)
	_ = d.Write("k", "v2", 2)
	chk(errors.Is(d.Write("k", "v3", 3), ErrHintOverflow), "overflow")
	chk(len(d.Hints(0)) == 2 && d.Get("k")[1] == E("v2", 2), "notrace")
	ad, _, _ := d.Up(0)
	chk(ad == 2 && d.Get("k")[0] == E("v2", 2), "usable")
	if bad != "" {
		return errors.New("api selfcheck: " + bad)
	}
	return nil
}
