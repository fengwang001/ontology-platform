package view

import (
	"errors"
	"fmt"
	"math"

	"ontology/agg"
	"ontology/change"
)

var (
	ErrVersionBack  = errors.New("version regression")
	ErrVersionDup   = errors.New("duplicate version with different payload")
	ErrMissingGroup = errors.New("missing group")
	ErrNaNValue     = errors.New("NaN value")
	ErrUnknownID    = errors.New("unknown record id")
)

type CrashPoint string

const (
	AfterApply   CrashPoint = "after_apply"
	InRecompute  CrashPoint = "in_recompute"
	BeforeCommit CrashPoint = "before_commit"
)

func (v *View) Submit(c change.Change) error {
	if err := Validate(c); err != nil {
		v.mu.Lock()
		v.rejected++
		v.mu.Unlock()
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if c.Version < v.version {
		v.rejected++
		return fmt.Errorf("%w: %d < %d", ErrVersionBack, c.Version, v.version)
	}
	if c.Version == v.version {
		if !change.Equal(c, v.last) {
			v.rejected++
			return ErrVersionDup
		}
		return nil
	}
	if v.journal != nil {
		if err := v.journal.AppendPending(c); err != nil {
			v.rejected++
			return err
		}
	}
	saved := v.snapshotState()
	if err := v.advance(c, true); err != nil {
		if v.journal != nil {
			_ = v.journal.DiscardPending()
		}
		v.restoreState(saved)
		return err
	}
	if v.journal != nil {
		if err := v.journal.MarkCommit(); err != nil {
			v.restoreState(saved)
			return err
		}
	}
	return nil
}

func Validate(c change.Change) error {
	if c.Version == 0 || c.Op < change.Insert || c.Op > change.Update || c.ID == "" {
		return errors.New("invalid change")
	}
	if !c.HasGroup || (c.Op == change.Update && !c.NewHasGrp) {
		return ErrMissingGroup
	}
	if math.IsNaN(c.Value) || (c.Op == change.Update && math.IsNaN(c.NewValue)) {
		return ErrNaNValue
	}
	return nil
}

func (v *View) advance(c change.Change, hook bool) error {
	need := map[string]map[agg.Kind]bool{}
	touch := func(g string) {
		if need[g] == nil {
			need[g] = map[agg.Kind]bool{}
		}
	}
	ask := func(g string, k agg.Kind) { touch(g); need[g][k] = true }
	switch c.Op {
	case change.Insert:
		if _, ok := v.records[c.ID]; ok {
			v.rejected++
			return errors.New("duplicate record id")
		}
		v.insert(c.Group, c.ID, c.Value)
	case change.Delete:
		m, ok := v.records[c.ID]
		if !ok {
			v.rejected++
			return ErrUnknownID
		}
		touch(m.group)
		for _, k := range v.remove(m.group, c.ID, m.value) {
			ask(m.group, k)
		}
	case change.Update:
		m, ok := v.records[c.ID]
		if !ok {
			v.rejected++
			return ErrUnknownID
		}
		touch(m.group)
		for _, k := range v.remove(m.group, c.ID, m.value) {
			ask(m.group, k)
		}
		v.insert(c.NewGroup, c.ID, c.NewValue)
	}
	if hook && v.crash == AfterApply {
		return errors.New("injected crash after apply")
	}
	v.recompute(need, hook)
	if hook && v.crash == BeforeCommit {
		return errors.New("injected crash before commit")
	}
	v.version = c.Version
	v.last = c
	return nil
}

func norm(x float64) float64 {
	if x == 0 {
		return 0
	}
	return x
}

func (v *View) ensureGroup(g string) *groupState {
	gs := v.groups[g]
	if gs == nil {
		gs = &groupState{members: map[string]float64{}, aggs: map[agg.Kind]agg.Aggregator{}}
		for _, k := range agg.All() {
			gs.aggs[k] = agg.New(k)
		}
		v.groups[g] = gs
	}
	return gs
}

func (v *View) insert(g, id string, value float64) {
	value = norm(value)
	gs := v.ensureGroup(g)
	gs.members[id] = value
	for _, a := range gs.aggs {
		a.Insert(value)
	}
	v.records[id] = member{g, value}
}

func (v *View) remove(g, id string, value float64) []agg.Kind {
	value = norm(value)
	gs := v.groups[g]
	delete(gs.members, id)
	delete(v.records, id)
	need := []agg.Kind{}
	for _, k := range agg.All() {
		if gs.aggs[k].Delete(value) {
			need = append(need, k)
		}
	}
	return need
}

func (v *View) recompute(need map[string]map[agg.Kind]bool, hook bool) {
	for g, kinds := range need {
		gs := v.groups[g]
		ms := make([]agg.Member, 0, len(gs.members))
		for id, value := range gs.members {
			ms = append(ms, agg.Member{ID: id, Value: value})
		}
		for k := range kinds {
			v.recomp[k]++
			v.scanned[k] += len(ms)
			gs.aggs[k].Recompute(ms)
			if hook && v.crash == InRecompute {
				return
			}
		}
		if len(gs.members) == 0 {
			delete(v.groups, g)
		}
	}
}
