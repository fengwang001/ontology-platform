package reconcile

import "sort"

// naiveReconcile is the deliberately simple, step-by-step reference model
// used to cross-check the optimized Reconciler. It trades efficiency for
// obviousness: every rule is applied literally, in isolation, with no
// shared state between steps. The property test feeds identical inputs to
// both implementations and requires identical verdicts.

// naiveReconcile mirrors the specification one rule at a time.
func naiveReconcile(snaps []*Snapshot) (baseline Position, objects map[string]map[string]string, irreconcilable []string) {
	// Rule 1: baseline is the earliest position among participants.
	first := true
	for _, s := range snaps {
		if first || s.Pos < baseline {
			baseline = s.Pos
			first = false
		}
	}

	// Rule 2: per object, per property, gather every candidate written at
	// or before the baseline.
	type cand struct {
		priority uint64
		value    string
	}
	gather := map[string]map[string][]cand{}
	for _, s := range snaps {
		for objID, obj := range s.Objects {
			for prop, vv := range obj.Props {
				if vv.WrittenAt > baseline {
					continue
				}
				props := gather[objID]
				if props == nil {
					props = map[string][]cand{}
					gather[objID] = props
				}
				props[prop] = append(props[prop], cand{priority: s.Replica.Priority, value: vv.Value})
			}
		}
	}

	// Rule 3: distinct values trigger the fixed rule (smallest priority
	// wins); distinct values tied at the best priority are irreconcilable.
	objects = map[string]map[string]string{}
	objIDs := make([]string, 0, len(gather))
	for objID := range gather {
		objIDs = append(objIDs, objID)
	}
	sort.Strings(objIDs)
	for _, objID := range objIDs {
		out := map[string]string{}
		bad := false
		for prop, cands := range gather[objID] {
			distinct := map[string]bool{}
			for _, c := range cands {
				distinct[c.value] = true
			}
			if len(distinct) == 1 {
				out[prop] = cands[0].value
				continue
			}
			best := cands[0].priority
			for _, c := range cands {
				if c.priority < best {
					best = c.priority
				}
			}
			topValues := map[string]bool{}
			for _, c := range cands {
				if c.priority == best {
					topValues[c.value] = true
				}
			}
			if len(topValues) > 1 {
				bad = true
				continue
			}
			for _, c := range cands {
				if c.priority == best {
					out[prop] = c.value
					break
				}
			}
		}
		if bad {
			irreconcilable = append(irreconcilable, objID)
		} else if len(out) > 0 {
			objects[objID] = out
		}
	}
	return baseline, objects, irreconcilable
}
