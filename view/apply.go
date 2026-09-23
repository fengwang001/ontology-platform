package view

import (
	"ontology/agg"
	"ontology/change"
)

// Apply validates and applies one change through Apply -> Recompute -> Commit
// atomically under the write lock. A duplicate version is an idempotent no-op;
// an older version is rejected. A Hook may panic with CrashPanic inside any
// phase; the caller discards the instance and recovers from the journal.
func (v *View) Apply(c change.Change) (err error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if c.Version < v.lastVersion {
		v.reject()
		return ErrVersionRollback
	}
	if c.Version == v.lastVersion && v.lastVersion != 0 {
		return nil // idempotent replay: field-by-field unchanged
	}
	if err := Validate(c); err != nil {
		v.reject()
		return err
	}

	defer func() {
		if r := recover(); r != nil {
			if cp, ok := r.(CrashPanic); ok {
				err = cp
				return
			}
			panic(r)
		}
	}()

	dirty := map[string]map[int]bool{}
	switch c.Op {
	case change.Insert:
		if _, exists := v.members[c.ID]; exists {
			v.reject()
			return ErrUnknownID
		}
		v.insert(c.ID, *c.Group, c.Value)
	case change.Delete:
		m, ok := v.members[c.ID]
		if !ok {
			v.reject()
			return ErrUnknownID
		}
		v.remove(c.ID, m.group, m.value, dirty)
	case change.Update:
		m, ok := v.members[c.ID]
		if !ok {
			v.reject()
			return ErrUnknownID
		}
		v.remove(c.ID, m.group, m.value, dirty)
		v.insert(c.ID, *c.NewGroup, c.NewValue)
	default:
		v.reject()
		return change.ErrUnknownOp
	}
	v.crash(PhaseApply)

	v.recomputeDirty(dirty)

	v.lastVersion = c.Version // Commit: publish version boundary
	v.crash(PhaseCommit)
	return nil
}

func (v *View) crash(phase string) {
	if v.Hook != nil {
		v.Hook(phase)
	}
}

var _ agg.Aggregator
