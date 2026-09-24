// Package clog implements the two-phase commit log, the visible view,
// crash semantics and recovery adjudication. It depends only on stg.
package clog

import "ontology/stg"

// State is the lifecycle state of a log entry.
type State int

const (
	// Prepared: step 1 done (changes logged), step 2 (apply) not done.
	Prepared State = iota
	// Finalized: changes applied to the visible view.
	Finalized
	// Discarded: adjudicated by Recover as never having happened.
	Discarded
)

func (s State) String() string {
	switch s {
	case Prepared:
		return "prepared"
	case Finalized:
		return "finalized"
	default:
		return "discarded"
	}
}

// Entry is one commit-log record.
type Entry struct {
	ID      int
	Changes []stg.Change
	State   State
}

// Log is the commit log plus the materialized visible view.
type Log struct {
	view      map[string]string
	entries   []Entry
	nextID    int
	recovered int // high-water mark: recovery never revisits ids <= this
	keyVisits int // view key accesses by the most recent finalize step
}

// New returns an empty Log with an empty view.
func New() *Log { return &Log{view: map[string]string{}, nextID: 1} }

// ViewHas reports whether k exists in the visible view.
func (l *Log) ViewHas(k string) bool { _, ok := l.view[k]; return ok }

// View returns a copy of the visible view: finalized commits only, in order.
func (l *Log) View() map[string]string {
	out := make(map[string]string, len(l.view))
	for k, v := range l.view {
		out[k] = v
	}
	return out
}

// Entries returns a copy of the log entries, in commit-number order.
func (l *Log) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// appendPrepared is step 1 of the two-phase commit: log the changes.
func (l *Log) appendPrepared(changes []stg.Change) int {
	id := l.nextID
	l.nextID++
	cp := make([]stg.Change, len(changes))
	copy(cp, changes)
	l.entries = append(l.entries, Entry{ID: id, Changes: cp, State: Prepared})
	return id
}

// apply is step 2: merge the changes into the visible view in place.
// It touches exactly one view key per staged change — never a full rebuild.
func (l *Log) apply(changes []stg.Change) {
	l.keyVisits = 0
	for _, c := range changes {
		if c.Del {
			delete(l.view, c.Key)
		} else {
			l.view[c.Key] = c.Val
		}
		l.keyVisits++
	}
}

// Commit runs both phases and returns the commit number (from 1, increasing).
func (l *Log) Commit(changes []stg.Change) int {
	id := l.appendPrepared(changes)
	l.apply(changes)
	l.entries[len(l.entries)-1].State = Finalized
	return id
}

// CommitCrash simulates a crash between the phases: the entry stays
// prepared, the view is untouched, the commit number is still consumed.
func (l *Log) CommitCrash(changes []stg.Change) int {
	return l.appendPrepared(changes)
}

// Recover adjudicates the newest prepared-not-finalized entry (above the
// recovery high-water mark) as discarded and returns its commit number.
// With nothing pending it returns 0; consecutive calls are idempotent.
func (l *Log) Recover() int {
	best := -1
	for i := range l.entries {
		if e := &l.entries[i]; e.State == Prepared && e.ID > l.recovered {
			if best < 0 || e.ID > l.entries[best].ID {
				best = i
			}
		}
	}
	if best < 0 {
		return 0
	}
	l.entries[best].State = Discarded
	l.recovered = l.entries[best].ID
	return l.entries[best].ID
}
