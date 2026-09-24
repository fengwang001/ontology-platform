// Package api is the public face: a version-conditioned upsert table with
// tombstones, backed by package store.
package api

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"

	"ontology/rule"
	"ontology/store"
)

// Event and Row are the store's wire types, re-exported.
type (
	Event = store.Event
	Row   = store.Row
)

// Distinguishable sentinel errors.
var (
	ErrInvalidParam = errors.New("api: invalid parameter")
	ErrInvalidEvent = store.ErrInvalidEvent
	ErrTooManyKeys  = store.ErrTooManyKeys
)

// Table is the public handle.
type Table struct{ s *store.Store }

// New builds a table; R >= 1 and maxKeys >= 1 are required.
func New(R int64, maxKeys int) (*Table, error) {
	if R < 1 || maxKeys < 1 {
		return nil, ErrInvalidParam
	}
	return &Table{s: store.New(R, maxKeys)}, nil
}

// Apply commits evs as one atomic batch.
func (t *Table) Apply(evs []Event) error { return t.s.Batch(evs) }

// Get returns the live row for key.
func (t *Table) Get(key string) (r Row, ok bool) { r, ok = t.s.GetMany([]string{key})[key]; return }

// GetMany returns a consistent same-instant snapshot of all named keys.
func (t *Table) GetMany(keys []string) map[string]Row { return t.s.GetMany(keys) }

// Tomb returns the tombstone version for key.
func (t *Table) Tomb(key string) (int64, bool) { return t.s.Tomb(key) }

// Stats returns the ignored-event count and the watermark G.
func (t *Table) Stats() (ignored, g int64) { return t.s.Stats() }

// naive is the reference model: same rules, tombstones never purged.
type naive struct {
	rows  map[string]Row
	tombs map[string]int64
	g     int64
}

func (n *naive) run(e Event) {
	cur := int64(0)
	if r, ok := n.rows[e.Key]; ok {
		cur = r.Ver
	} else if t, ok := n.tombs[e.Key]; ok {
		cur = t
	}
	if rule.Applies(e.Ver, cur) {
		if e.Op == 'U' {
			n.rows[e.Key] = Row{Val: e.Val, Ver: e.Ver}
			delete(n.tombs, e.Key)
		} else {
			delete(n.rows, e.Key)
			n.tombs[e.Key] = e.Ver
		}
	}
	n.g = max(n.g, e.Ver)
}

// SelfCheck verifies the four invariants on built-in event sequences.
func (t *Table) SelfCheck() error {
	// Invariant 1+3: monotone versions; a tombstone blocks older upserts.
	ck, _ := New(1<<60, 100)
	seq := []Event{
		{Op: 'U', Key: "a", Val: "v1", Ver: 5}, {Op: 'D', Key: "a", Ver: 8},
		{Op: 'U', Key: "a", Val: "old", Ver: 6}, {Op: 'U', Key: "a", Val: "eq", Ver: 8},
	}
	for _, e := range seq {
		if err := ck.Apply([]Event{e}); err != nil {
			return err
		}
	}
	if _, live := ck.Get("a"); live {
		return errors.New("selfcheck: stale upsert revived a deleted key")
	}
	if tv, ok := ck.Tomb("a"); !ok || tv != 8 {
		return errors.New("selfcheck: tombstone version regressed")
	}
	if ign, _ := ck.Stats(); ign != 2 {
		return errors.New("selfcheck: ignored count wrong")
	}
	// Invariant 2: streams with Ver > G_before-R match the naive reference.
	for trial := 0; trial < 200; trial++ {
		got, _ := New(10, 1000)
		ref := naive{rows: map[string]Row{}, tombs: map[string]int64{}}
		for n := 0; n < 60; n++ {
			lo := max(ref.g-9, 1)
			e := Event{Op: 'U', Key: "k" + strconv.Itoa(rand.IntN(6)), Ver: lo + rand.Int64N(ref.g+6-lo), Val: "x"}
			if rand.IntN(4) == 0 {
				e.Op = 'D'
			}
			if err := got.Apply([]Event{e}); err != nil {
				return err
			}
			ref.run(e)
			for k, want := range ref.rows {
				r, ok := got.Get(k)
				if !ok || r != want {
					return fmt.Errorf("selfcheck: row mismatch on %s", k)
				}
			}
		}
	}
	// Invariant 4: a rejected batch leaves no trace.
	b4, _ := New(10, 1)
	if err := b4.Apply([]Event{{Op: 'U', Key: "a", Val: "1", Ver: 1}}); err != nil {
		return err
	}
	if err := b4.Apply([]Event{{Op: 'U', Key: "b", Val: "2", Ver: 2}}); !errors.Is(err, ErrTooManyKeys) {
		return errors.New("selfcheck: overflow not reported")
	}
	if err := b4.Apply([]Event{{Op: 'U', Key: "", Val: "x", Ver: 3}}); !errors.Is(err, ErrInvalidEvent) {
		return errors.New("selfcheck: invalid event not reported")
	}
	if _, ok := b4.Get("b"); ok {
		return errors.New("selfcheck: rejected batch left a row")
	}
	if ign, g := b4.Stats(); ign != 0 || g != 1 {
		return errors.New("selfcheck: rejected batch moved stats")
	}
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidParam) {
		return errors.New("selfcheck: bad params accepted")
	}
	if !store.PurgeCostBound() {
		return errors.New("selfcheck: purge cost grows with tombstone count")
	}
	return nil
}
