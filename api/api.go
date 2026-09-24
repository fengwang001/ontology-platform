// Package api is the public face of the staging committer: staged changes
// stay invisible until an atomic two-phase commit makes them visible.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/clog"
	"ontology/stg"
)

// Sentinel errors: each rejection is decidable and distinct.
var (
	ErrEmptyKey     = errors.New("api: empty key")
	ErrEmptyCommit  = errors.New("api: commit with empty staging area")
	ErrNotDeletable = errors.New("api: delete target not deletable")
)

// Committer stages key/value mutations and commits them atomically.
type Committer struct {
	mu    sync.RWMutex
	stage *stg.Stage
	log   *clog.Log
}

// New returns a Committer with empty view, staging and log.
func New() *Committer { return &Committer{stage: stg.New(), log: clog.New()} }

// Put stages an upsert; an already-staged key is overwritten.
func (c *Committer) Put(k, v string) error {
	if k == "" {
		return ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stage.Put(k, v)
	return nil
}

// Del stages a delete; the key must exist in the view and be unstaged.
func (c *Committer) Del(k string) error {
	if k == "" {
		return ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage.Has(k) || !c.log.ViewHas(k) {
		return ErrNotDeletable
	}
	c.stage.Del(k)
	return nil
}

// Commit two-phase-commits the staging area; empty staging is rejected.
func (c *Committer) Commit() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage.Len() == 0 {
		return 0, ErrEmptyCommit
	}
	return c.log.Commit(c.stage.Take()), nil
}

// CommitCrash commits but crashes between the phases: the entry stays
// prepared, the view is untouched, the commit number is consumed.
func (c *Committer) CommitCrash() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage.Len() == 0 {
		return 0, ErrEmptyCommit
	}
	return c.log.CommitCrash(c.stage.Take()), nil
}

// Rollback clears the staging area; it always succeeds, leaving no trace.
func (c *Committer) Rollback() { c.mu.Lock(); c.stage.Clear(); c.mu.Unlock() }

// Recover adjudicates the newest half-committed entry as discarded.
func (c *Committer) Recover() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.log.Recover()
}

// View returns the visible view: finalized commits only, in order.
func (c *Committer) View() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.log.View()
}

// Staged returns an ordered snapshot of the staging area.
func (c *Committer) Staged() []stg.Change {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stage.Snapshot()
}

// Entries returns the commit-log entries with their states.
func (c *Committer) Entries() []clog.Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.log.Entries()
}

// SelfCheck verifies the four invariants on built-in operation sequences.
// It uses only fresh instances, so it is safe for concurrent use.
func (c *Committer) SelfCheck() error {
	m, view := New(), map[string]string{}
	seed := uint32(378)
	rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % n }
	keys := []string{"k1", "k2", "k3", "k4"}
	for i := 0; i < 300; i++ { // invariants 1-3: model replays finalized only
		staged := map[string]bool{}
		for _, ch := range m.Staged() {
			staged[ch.Key] = true
		}
		switch rnd(6) {
		case 0, 1:
			if err := m.Put(keys[rnd(4)], fmt.Sprint(i)); err != nil {
				return err
			}
		case 2:
			for _, k := range keys {
				if _, ok := view[k]; ok && !staged[k] {
					if err := m.Del(k); err != nil {
						return err
					}
					break
				}
			}
		case 3, 4:
			if len(m.Staged()) == 0 {
				continue
			}
			if i%2 == 0 { // commit: model applies the staged changes
				for _, ch := range m.Staged() {
					if ch.Del {
						delete(view, ch.Key)
					} else {
						view[ch.Key] = ch.Val
					}
				}
				if _, err := m.Commit(); err != nil {
					return err
				}
			} else if _, err := m.CommitCrash(); err != nil { // model unchanged
				return err
			}
		case 5:
			if rnd(2) == 0 {
				m.Rollback()
			} else {
				m.Recover()
			}
		}
		if !reflect.DeepEqual(m.View(), view) {
			return fmt.Errorf("selfcheck: view diverged at op %d", i)
		}
	}
	return checkRejections(m)
}

// checkRejections verifies invariant 4: rejections are decidable, distinct
// from success, and leave staging, view, log and numbering untouched.
func checkRejections(m *Committer) error {
	if _, err := New().Commit(); !errors.Is(err, ErrEmptyCommit) {
		return fmt.Errorf("selfcheck: empty commit not rejected")
	}
	if err := m.Put("zz", "1"); err != nil {
		return err
	}
	if err := m.Del("zz"); !errors.Is(err, ErrNotDeletable) {
		return fmt.Errorf("selfcheck: delete of staged key not rejected")
	}
	m.Rollback()
	v, s, e := m.View(), m.Staged(), m.Entries()
	for _, err := range []error{m.Put("", "x"), m.Del(""), m.Del("no-such-key")} {
		if !errors.Is(err, ErrEmptyKey) && !errors.Is(err, ErrNotDeletable) {
			return fmt.Errorf("selfcheck: bad rejection: %v", err)
		}
	}
	if !reflect.DeepEqual(m.View(), v) || !reflect.DeepEqual(m.Staged(), s) ||
		!reflect.DeepEqual(m.Entries(), e) {
		return fmt.Errorf("selfcheck: rejection left a trace")
	}
	return nil
}
