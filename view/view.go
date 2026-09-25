// Package view maintains grouped aggregate state with retractable inserts,
// deletes and updates, persisting each change to a CRC journal for recovery.
package view

import (
	"errors"
	"math"
	"os"
	"sync"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

// ErrVersionOrder is returned (and counted) for out-of-order versions.
var ErrVersionOrder = errors.New("view: version regression")

// CrashPoint selects an injected panic inside Submit (before Commit).
type CrashPoint int

const (
	CrashNone CrashPoint = iota
	CrashAfterApply
	CrashDuringRecompute
	CrashBeforeCommit
)

// Stats exposes the recompute counters and rejection count.
type Stats struct {
	RecomputeRuns    int64
	RecomputeMembers int64
	Rejected         int64
}

type group struct {
	members map[string]float64
	aggs    []agg.Aggregator
}

// View is the incremental grouped view.
type View struct {
	mu        sync.Mutex
	path      string
	j         *journal.Writer
	names     []string
	groups    map[string]*group
	lastVer   uint64
	recompR   int64
	recompM   int64
	rejected  int64
	nextCrash CrashPoint
}

// New creates a fresh journal-backed view.
func New(path string, names []string) (*View, error) {
	w, err := journal.Create(path)
	if err != nil {
		return nil, err
	}
	return &View{path: path, j: w, names: names, groups: map[string]*group{}}, nil
}

// Open recovers a view from its journal and appends subsequent changes.
func Open(path string, names []string) (*View, error) {
	v := &View{path: path, names: names, groups: map[string]*group{}}
	_, err := journal.Replay(path, func(c change.Change) error {
		return v.apply(c, CrashNone)
	})
	if err != nil && !errors.Is(err, journal.ErrCRCMismatch) &&
		!errors.Is(err, journal.ErrLenPrefixIncomplete) &&
		!errors.Is(err, journal.ErrBodyIncomplete) &&
		!errors.Is(err, journal.ErrHeaderIncomplete) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	// Recovered logs may lack an END marker; appending still continues safely.
	v.j = &journal.Writer{}
	v.attachWriter(f)
	return v, nil
}

// Close syncs and closes the underlying journal.
func (v *View) Close() error { return v.j.Close() }

// SetCrash arms a one-shot panic at the given point for the next Submit.
func (v *View) SetCrash(p CrashPoint) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.nextCrash = p
}

func (v *View) newAggs() []agg.Aggregator {
	out := make([]agg.Aggregator, len(v.names))
	for i, n := range v.names {
		out[i] = agg.New(n)
	}
	return out
}

func (v *View) grp(name string) *group {
	g := v.groups[name]
	if g == nil {
		g = &group{members: map[string]float64{}, aggs: v.newAggs()}
		v.groups[name] = g
	}
	return g
}

func (v *View) recompute(g *group) {
	v.recompR++
	values := make([]float64, 0, len(g.members))
	for _, val := range g.members {
		values = append(values, val)
	}
	v.recompM += int64(len(values))
	for _, a := range g.aggs {
		if a.NeedsMembersOnDelete() {
			a.Recompute(values)
		}
	}
}

func insertInto(g *group, key string, val float64) {
	g.members[key] = val
	for _, a := range g.aggs {
		a.Insert(val)
	}
}

// apply runs Apply -> Recompute -> Commit for one change (caller holds lock).
func (v *View) apply(c change.Change, crash CrashPoint) error {
	switch c.Op {
	case change.OpInsert:
		g := v.grp(c.Group)
		insertInto(g, c.RKey, c.Value)
	case change.OpDelete:
		g := v.groups[c.Group]
		if g != nil {
			if old, ok := g.members[c.RKey]; ok {
				delete(g.members, c.RKey)
				need := false
				for _, a := range g.aggs {
					a.Delete(old)
					need = need || a.NeedsMembersOnDelete()
				}
				if crash == CrashDuringRecompute {
					panic("crash during recompute")
				}
				if need && len(g.members) > 0 {
					v.recompute(g)
				}
				if len(g.members) == 0 {
					delete(v.groups, c.Group)
				}
			}
		}
	case change.OpUpdate:
		if c.HasOld {
			if og := v.groups[c.OldGroup]; og != nil {
				if old, ok := og.members[c.RKey]; ok {
					delete(og.members, c.RKey)
					need := false
					for _, a := range og.aggs {
						a.Delete(old)
						need = need || a.NeedsMembersOnDelete()
					}
					if need && len(og.members) > 0 {
						v.recompute(og)
					}
					if len(og.members) == 0 {
						delete(v.groups, c.OldGroup)
					}
				}
			}
		}
		insertInto(v.grp(c.Group), c.RKey, c.Value)
	}
	v.lastVer = c.Version
	return nil
}

// Submit validates, journals, then applies one change atomically.
func (v *View) Submit(c change.Change) (err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := c.Validate(); err != nil {
		v.rejected++
		return err
	}
	if c.Version < v.lastVer || (c.Version == v.lastVer && c.Version != 0) {
		v.rejected++
		return ErrVersionOrder
	}
	crash := v.nextCrash
	v.nextCrash = CrashNone
	if err := v.j.Append(c); err != nil {
		return err
	}
	if crash == CrashAfterApply {
		panic("crash after apply")
	}
	if err := v.apply(c, crash); err != nil {
		return err
	}
	if crash == CrashBeforeCommit {
		panic("crash before commit")
	}
	return nil
}

func aggregateResults(g *group) map[string]uint64 {
	out := make(map[string]uint64, len(g.aggs))
	for _, a := range g.aggs {
		val, ok := a.Result()
		if ok {
			out[a.Name()] = math.Float64bits(val)
		}
	}
	return out
}

// Snapshot returns an immutable deep copy of all groups as bit-pattern results.
func (v *View) Snapshot() map[string]map[string]uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make(map[string]map[string]uint64, len(v.groups))
	for name, g := range v.groups {
		out[name] = aggregateResults(g)
	}
	return out
}

// Get returns one group's aggregate bits and false if the group does not exist.
func (v *View) Get(group string) (map[string]uint64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	g := v.groups[group]
	if g == nil {
		return nil, false
	}
	return aggregateResults(g), true
}

// Groups returns the current group keys.
func (v *View) Groups() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]string, 0, len(v.groups))
	for name := range v.groups {
		out = append(out, name)
	}
	return out
}

// Stats returns the recompute and rejection counters.
func (v *View) Stats() Stats {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Stats{RecomputeRuns: v.recompR, RecomputeMembers: v.recompM, Rejected: v.rejected}
}

// LastVersion returns the highest applied version.
func (v *View) LastVersion() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastVer
}
