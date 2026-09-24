// Package api is the public entry point: Commit/Expire, inspection and SelfCheck.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/fileref"
	"ontology/snapchain"
)

// Table is one in-memory table; mu guards chain and store as one unit.
type Table struct {
	mu    sync.RWMutex
	chain *snapchain.Chain
	store *fileref.Store
}

func New() *Table { return &Table{chain: snapchain.New(), store: fileref.New()} }

// Commit validates every rule before mutating; a rejection leaves no trace
// in chain, reference counts, file store or the seen-name register.
func (t *Table) Commit(ts int64, add, remove []string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.chain.CheckCommit(ts, remove); err != nil {
		return 0, err
	}
	if err := t.store.CheckCommit(add, remove); err != nil {
		return 0, err
	}
	var inherited []string
	if cur, ok := t.chain.Current(); ok {
		removed := map[string]struct{}{}
		for _, f := range remove {
			removed[f] = struct{}{}
		}
		inherited = make([]string, 0, len(cur.Files))
		for _, f := range cur.Files {
			if _, drop := removed[f]; !drop {
				inherited = append(inherited, f)
			}
		}
	}
	id, _ := t.chain.Commit(ts, add, remove)
	t.store.Commit(add, inherited)
	return id, nil
}
func (t *Table) Expire(N int, T int64) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.store.Expire(t.chain, N, T)
}
func (t *Table) Snapshots() []snapchain.Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.chain.Snapshots()
}
func (t *Table) Files() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.store.Files()
}

// State returns snapshots and files from one coherent instant under a single
// read lock, so concurrent readers never mix a pre-Expire snapshot list with
// a post-Expire file set.
func (t *Table) State() ([]snapchain.Snapshot, []string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.chain.Snapshots(), t.store.Files()
}

var errSelfCheck = errors.New("selfcheck failed")

// SelfCheck verifies the four invariants on the spec's eight-step sequence
// (expected states are the naive per-rule results from NOTES.md), then the
// four distinct detectable rejections and the no-trace/usable guarantees.
func (t *Table) SelfCheck() error {
	tab := New()
	type row struct {
		exp                  bool
		n                    int
		ts                   int64
		add, rem, del, files []string
		snaps                []snapchain.Snapshot
	}
	s := func(id int, ts int64, files ...string) snapchain.Snapshot {
		return snapchain.Snapshot{ID: id, TS: ts, Files: files}
	}
	rows := []row{
		{ts: 10, add: ss("f1", "f2"), snaps: []snapchain.Snapshot{s(1, 10, "f1", "f2")}, files: ss("f1", "f2")},
		{ts: 20, add: ss("f3"), rem: ss("f1"), snaps: []snapchain.Snapshot{s(1, 10, "f1", "f2"), s(2, 20, "f2", "f3")}, files: ss("f1", "f2", "f3")},
		{ts: 30, add: ss("f4"), rem: ss("f2"), snaps: []snapchain.Snapshot{s(1, 10, "f1", "f2"), s(2, 20, "f2", "f3"), s(3, 30, "f3", "f4")}, files: ss("f1", "f2", "f3", "f4")},
		{ts: 40, add: ss("f5"), rem: ss("f3"), snaps: []snapchain.Snapshot{s(1, 10, "f1", "f2"), s(2, 20, "f2", "f3"), s(3, 30, "f3", "f4"), s(4, 40, "f4", "f5")}, files: ss("f1", "f2", "f3", "f4", "f5")},
		{exp: true, n: 1, ts: 15, del: ss("f1"), snaps: []snapchain.Snapshot{s(2, 20, "f2", "f3"), s(3, 30, "f3", "f4"), s(4, 40, "f4", "f5")}, files: ss("f2", "f3", "f4", "f5")},
		{ts: 50, add: ss("f6"), rem: ss("f4"), snaps: []snapchain.Snapshot{s(2, 20, "f2", "f3"), s(3, 30, "f3", "f4"), s(4, 40, "f4", "f5"), s(5, 50, "f5", "f6")}, files: ss("f2", "f3", "f4", "f5", "f6")},
		{exp: true, n: 2, ts: 35, del: ss("f2", "f3"), snaps: []snapchain.Snapshot{s(4, 40, "f4", "f5"), s(5, 50, "f5", "f6")}, files: ss("f4", "f5", "f6")},
		{exp: true, n: 0, ts: 50, del: ss("f4"), snaps: []snapchain.Snapshot{s(5, 50, "f5", "f6")}, files: ss("f5", "f6")},
	}
	for _, r := range rows {
		var del []string
		var err error
		if r.exp {
			del, err = tab.Expire(r.n, r.ts)
		} else {
			_, err = tab.Commit(r.ts, r.add, r.rem)
		}
		if err != nil || !reflect.DeepEqual(del, r.del) || !reflect.DeepEqual(tab.Snapshots(), r.snaps) || !reflect.DeepEqual(tab.Files(), r.files) {
			if err != nil {
				return err
			}
			return errSelfCheck
		}
	}
	before := tab.Snapshots()
	for _, r := range []struct {
		exp      bool
		n        int
		ts       int64
		add, rem []string
		want     error
	}{
		{exp: true, n: -1, want: fileref.ErrNegativeN},
		{ts: 50, add: ss("x"), want: snapchain.ErrTSNotIncreasing},
		{ts: 60, add: ss("f1"), want: fileref.ErrNameConflict},
		{ts: 60, rem: ss("f1"), want: snapchain.ErrFileNotInCurrent},
	} {
		var e error
		if r.exp {
			_, e = tab.Expire(r.n, r.ts)
		} else {
			_, e = tab.Commit(r.ts, r.add, r.rem)
		}
		if !errors.Is(e, r.want) {
			return errSelfCheck
		}
	}
	if !reflect.DeepEqual(tab.Snapshots(), before) || !reflect.DeepEqual(tab.Files(), ss("f5", "f6")) {
		return errSelfCheck
	}
	if _, err := tab.Commit(60, ss("g1"), ss("f5")); err != nil {
		return errSelfCheck
	}
	return nil
}

func ss(v ...string) []string { return v }
