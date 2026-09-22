package view

import (
	"errors"

	"ontology/change"
)

// Sentinel errors. Use errors.Is for classification.
var (
	// ErrVersionBackward is returned when a change's version is lower
	// than the greatest applied version.
	ErrVersionBackward = errors.New("view: version backward")
	// ErrVersionConflict is returned when a previously seen version is
	// redelivered carrying a different change.
	ErrVersionConflict = errors.New("view: version reused with different change")
	// ErrInvalidChange is returned for missing group keys or NaN values.
	ErrInvalidChange = errors.New("view: invalid change")
	// ErrUnknownKey is returned when a delete/update references a key
	// that is not currently present.
	ErrUnknownKey = errors.New("view: unknown member key")
)

// Submit applies one versioned change through Apply -> Recompute ->
// Commit. Duplicate (same-version, same-payload) delivery is idempotent.
// Backward versions, missing groups and NaN are rejected and counted in
// Stats without mutating the view.
func (v *View) Submit(c change.Change) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if c.Version < v.maxVersion {
		v.rejected++
		v.stats.Rejected++
		return ErrVersionBackward
	}
	if c.Version == v.maxVersion {
		if fp, ok := v.versionSeen[c.Version]; ok {
			if fp == fingerprint(c) {
				v.stats.Reapplied++
				return nil
			}
			v.rejected++
			v.stats.Rejected++
			return ErrVersionConflict
		}
	}
	if !v.semanticallyValid(c) {
		v.rejected++
		v.stats.Rejected++
		return ErrInvalidChange
	}
	if err := v.checkKeys(c); err != nil {
		v.rejected++
		v.stats.Rejected++
		return err
	}

	// Phase: Apply (mutate membership, mark affected aggregators).
	plan := v.planChange(c)
	v.applyPlan(plan)
	if v.hook != nil {
		if err := v.hook(PhaseApply, c); err != nil {
			return err
		}
	}

	// Phase: Recompute (only groups/aggregators that demanded members).
	v.recomputePlan(plan)
	if v.hook != nil {
		if err := v.hook(PhaseRecompute, c); err != nil {
			return err
		}
	}

	// Phase: Commit (durable append, then publish version).
	if v.hook != nil {
		if err := v.hook(PhasePreCommit, c); err != nil {
			return err
		}
	}
	if v.w != nil {
		if err := v.w.Append(c); err != nil {
			return err
		}
	}
	v.maxVersion = c.Version
	v.versionSeen[c.Version] = fingerprint(c)
	v.publishPlan(plan)
	return nil
}

func (v *View) semanticallyValid(c change.Change) bool {
	switch c.Op {
	case change.OpInsert, change.OpDelete:
		return validRow(c.From)
	case change.OpUpdate:
		return validRow(c.From) && validRow(c.To)
	default:
		return false
	}
}

func (v *View) checkKeys(c change.Change) error {
	switch c.Op {
	case change.OpInsert:
		if _, ok := v.members[c.Key]; ok {
			return ErrInvalidChange
		}
	case change.OpDelete, change.OpUpdate:
		if _, ok := v.members[c.Key]; !ok {
			return ErrUnknownKey
		}
	}
	return nil
}

// ensureGroup returns the live group state, creating it on first use.
func (v *View) ensureGroup(name string) *groupState {
	g := v.groups[name]
	if g == nil {
		g = newGroup()
		v.groups[name] = g
	}
	return g
}
