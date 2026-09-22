package view

import "ontology/change"

// applyReplay folds one journal record during recovery. Records in the
// journal are trusted (they survived Submit validation), so only the
// membership/aggregator pipeline and version watermark are restored;
// rejection counters stay zero.
func (v *View) applyReplay(c change.Change) error {
	p := v.planChange(c)
	v.applyPlan(p)
	v.recomputePlan(p)
	v.publishPlan(p)
	v.maxVersion = c.Version
	v.versionSeen[c.Version] = fingerprint(c)
	return nil
}
