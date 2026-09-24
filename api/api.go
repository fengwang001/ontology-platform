// Package api is the external entry point for tracking executed GTID
// transactions and computing what a source still needs to send.
package api

import (
	"errors"
	"sync"

	"ontology/gtid"
)

// Tracker is the in-process executed-set; safe for concurrent use.
type Tracker struct {
	mu   sync.RWMutex
	max  int
	exec gtid.Set
}

// New creates a Tracker rejecting merges whose normalized interval
// total would exceed maxIntervals.
func New(maxIntervals int) (*Tracker, error) {
	if maxIntervals < 1 {
		return nil, gtid.ErrRange
	}
	empty, _ := gtid.Parse("")
	return &Tracker{max: maxIntervals, exec: empty}, nil
}

// Apply parses text and merges it into the executed set atomically; a
// rejected text (bad syntax/UUID/range or interval overflow) changes
// nothing: the merged candidate is built and counted before it is kept.
func (t *Tracker) Apply(text string) error {
	add, err := gtid.Parse(text)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	cand := t.exec.Union(add)
	if cand.Intervals() > t.max {
		return gtid.ErrTooMany
	}
	t.exec = cand
	return nil
}

// Executed returns the canonical text of the executed set.
func (t *Tracker) Executed() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.exec.String()
}

// Missing returns source minus executed in canonical text; state and
// source are never mutated.
func (t *Tracker) Missing(source string) (string, error) {
	src, err := gtid.Parse(source)
	if err != nil {
		return "", err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.exec.Subtract(src).String(), nil
}

// SelfCheck verifies the four invariants on a set of built-in texts.
func (t *Tracker) SelfCheck() error {
	tr, err := New(16)
	if err != nil {
		return err
	}
	const u = "aaaaaaaa-1111-1111-1111-111111111111"
	const v = "bbbbbbbb-2222-2222-2222-222222222222"
	// Invariant 1 (naive reference, shuffled/overlapping/adjacent input).
	for _, tx := range []string{u + ":3-5:1:9-10", v + ":100:102", u + ":2:6-8", v + ":101:99-99"} {
		if err := tr.Apply(tx); err != nil {
			return err
		}
	}
	if got := tr.Executed(); got != u+":1-10,"+v+":99-102" {
		return errors.New("selfcheck: naive-union mismatch: " + got)
	}
	gap, err := tr.Missing(u + ":1-12," + v + ":99-103:200")
	if err != nil || gap != u+":11-12,"+v+":103:200" {
		return errors.New("selfcheck: naive-difference mismatch: " + gap)
	}
	// Invariant 2: canonical output survives a parse round-trip.
	if rt, _ := gtid.Parse(tr.Executed()); rt.String() != tr.Executed() {
		return errors.New("selfcheck: canonical form not stable")
	}
	// Invariant 3: applying the gap closes it and adds exactly the gap.
	before, _ := gtid.Parse(tr.Executed())
	gapSet, _ := gtid.Parse(gap)
	if err := tr.Apply(gap); err != nil {
		return err
	}
	if g2, _ := tr.Missing(u + ":1-12," + v + ":99-103:200"); g2 != "" {
		return errors.New("selfcheck: gap not closed: " + g2)
	}
	if after, _ := gtid.Parse(tr.Executed()); after.String() != before.Union(gapSet).String() {
		return errors.New("selfcheck: closing gap changed unrelated state")
	}
	// Invariant 4: all four distinct, decidable rejections leave no trace.
	bad := map[string]error{" \t": gtid.ErrSyntax, u + ":1,,": gtid.ErrSyntax,
		"not-a-uuid:1": gtid.ErrUUID, u + ":0": gtid.ErrRange, u + ":9-2": gtid.ErrRange}
	snap := tr.Executed()
	for text, want := range bad {
		if err := tr.Apply(text); !errors.Is(err, want) || tr.Executed() != snap {
			return errors.New("selfcheck: rejection leaked or misclassified: " + text)
		}
	}
	return nil
}
