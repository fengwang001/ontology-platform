// Package view maintains grouped aggregates incrementally with withdraw,
// journal-backed recovery and an Apply/Recompute/Commit pipeline.
package view

import (
	"errors"
	"path/filepath"
	"sync"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

// ErrVersionRollback is returned when a change version is below maxVer.
var ErrVersionRollback = errors.New("view: version rollback")

// ErrCrashed marks an injected pipeline crash; the view must be reopened.
var ErrCrashed = errors.New("view: injected crash")

// Group is one published per-group aggregate snapshot.
type Group struct {
	Count, Sum, Min, Max, Distinct float64
}

type state struct {
	members map[uint64]float64
	aggs    [agg.NumKinds]agg.Aggregator
}

func newState() *state {
	s := &state{members: map[uint64]float64{}}
	for k := agg.Kind(0); k < agg.NumKinds; k++ {
		s.aggs[k] = agg.New(k)
	}
	return s
}

// CrashPoint selects an injected failure during the pipeline.
type CrashPoint int

const (
	NoCrash CrashPoint = iota
	CrashInApply
	CrashInRecompute
	CrashBeforeCommit
)

// View is the in-memory aggregate state plus its write-ahead journal.
type View struct {
	mu          sync.RWMutex
	j           *journal.Journal
	groups      map[string]*state
	ids         map[uint64]string
	maxVer      uint64
	rejected    int64
	recomp      [agg.NumKinds]int64
	memberScans int64
	crash       CrashPoint
}

// New opens dir's journal (creating it) and rebuilds from its complete prefix.
func New(dir string, crash CrashPoint) (*View, error) {
	j, err := journal.Open(dir, 0)
	if err != nil {
		return nil, err
	}
	v := &View{j: j, groups: map[string]*state{}, ids: map[uint64]string{}, crash: crash}
	recs, _, err := journal.Replay(filepath.Join(dir, "view.journal"), -1)
	if err != nil && !errors.Is(err, journal.ErrShortHeader) {
		return nil, err
	}
	for _, c := range recs {
		v.replay(c)
	}
	return v, nil
}

// Close releases the journal file.
func (v *View) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.j == nil {
		return nil
	}
	err := v.j.Close()
	v.j = nil
	return err
}

// Rejected is the count of changes refused before touching state or journal.
func (v *View) Rejected() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return int(v.rejected)
}

// Recomputes reports how many times aggregator k triggered a Recompute.
func (v *View) Recomputes(k agg.Kind) int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.recomp[k]
}

// MemberScans reports total members visited during recomputes.
func (v *View) MemberScans() int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.memberScans
}

// MaxVer returns the highest applied version.
func (v *View) MaxVer() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.maxVer
}

// Query returns one group snapshot and whether it exists.
func (v *View) Query(g string) (Group, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	s, ok := v.groups[g]
	if !ok {
		return Group{}, false
	}
	return snapshot(s), true
}

// Groups returns a snapshot copy of every live group.
func (v *View) Groups() map[string]Group {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]Group, len(v.groups))
	for g, s := range v.groups {
		out[g] = snapshot(s)
	}
	return out
}

func snapshot(s *state) Group {
	return Group{
		Count: s.aggs[agg.Count].Value(), Sum: s.aggs[agg.Sum].Value(),
		Min: s.aggs[agg.Min].Value(), Max: s.aggs[agg.Max].Value(),
		Distinct: s.aggs[agg.DistinctCount].Value(),
	}
}

// Submit validates, durably appends, then runs Apply/Recompute/Commit.
func (v *View) Submit(c change.Change) error {
	if err := c.Validate(); err != nil {
		v.mu.Lock()
		v.rejected++
		v.mu.Unlock()
		return err
	}
	v.mu.Lock()
	if c.Ver < v.maxVer {
		v.rejected++
		v.mu.Unlock()
		return ErrVersionRollback
	}
	if c.Ver == v.maxVer {
		v.mu.Unlock()
		return nil
	}
	if c.Op == change.Insert {
		if _, dup := v.ids[c.RecID]; dup {
			v.rejected++
			v.mu.Unlock()
			return errors.New("view: duplicate record id")
		}
	}
	if err := v.j.Append(c); err != nil { // WAL durable before state changes
		v.mu.Unlock()
		return err
	}
	needs := map[string][agg.NumKinds]bool{}
	v.mutate(c, needs)
	v.rebuild(needs)
	if v.crash == CrashBeforeCommit {
		v.j.Close()
		return ErrCrashed // lock abandoned; reopen replays the WAL
	}
	v.maxVer = c.Ver
	v.mu.Unlock()
	return nil
}

func (v *View) replay(c change.Change) {
	v.mu.Lock()
	defer v.mu.Unlock()
	needs := map[string][agg.NumKinds]bool{}
	v.mutate(c, needs)
	v.rebuild(needs)
	v.maxVer = c.Ver
}

func (v *View) mutate(c change.Change, needs map[string][agg.NumKinds]bool) {
	oldG, exists := v.ids[c.RecID]
	switch c.Op {
	case change.Insert:
		v.insert(c.Group, c.RecID, c.Value)
	case change.Delete:
		if exists {
			v.withdraw(oldG, c.RecID, needs)
		}
	case change.Update:
		if exists {
			v.withdraw(oldG, c.RecID, needs)
		}
		v.insert(c.NewGroup, c.RecID, c.NewValue)
	}
	if v.crash == CrashInApply {
		panic("view: crash in apply")
	}
	v.prune(oldG, c)
}

func (v *View) insert(g string, id uint64, val float64) {
	s, ok := v.groups[g]
	if !ok {
		s = newState()
		v.groups[g] = s
	}
	s.members[id] = val
	v.ids[id] = g
	for k := range s.aggs {
		s.aggs[k].Insert(val)
	}
}

func (v *View) withdraw(g string, id uint64, needs map[string][agg.NumKinds]bool) {
	s, ok := v.groups[g]
	if !ok {
		return
	}
	val := s.members[id]
	delete(s.members, id)
	delete(v.ids, id)
	flag := needs[g]
	for k := range s.aggs {
		cur := s.aggs[k].Value()
		if s.aggs[k].Delete(val) || agg.NeedsMembersOnDelete(agg.Kind(k), val, cur) {
			flag[k] = true
		}
	}
	needs[g] = flag
}

func (v *View) rebuild(needs map[string][agg.NumKinds]bool) {
	for g, flag := range needs {
		s, ok := v.groups[g]
		if !ok {
			continue
		}
		var kinds []agg.Kind
		for k := range flag {
			if flag[k] {
				kinds = append(kinds, agg.Kind(k))
			}
		}
		for _, k := range kinds {
			s.aggs[k].Reset()
			v.recomp[k]++
		}
		for _, val := range s.members {
			v.memberScans++
			for _, k := range kinds {
				s.aggs[k].Insert(val)
			}
		}
	}
	if v.crash == CrashInRecompute {
		panic("view: crash in recompute")
	}
}

func (v *View) prune(oldG string, c change.Change) {
	groups := map[string]bool{oldG: true}
	switch c.Op {
	case change.Insert:
		groups[c.Group] = true
	case change.Update:
		groups[c.NewGroup] = true
	}
	for g := range groups {
		if s, ok := v.groups[g]; ok && len(s.members) == 0 {
			delete(v.groups, g)
		}
	}
}
