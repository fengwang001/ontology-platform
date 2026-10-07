package reconcile

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// ErrInsufficientReplicas is returned when the number of readable
// snapshots is below the minimum required to determine a baseline.
var ErrInsufficientReplicas = errors.New("reconcile: insufficient readable replicas to determine baseline")

// Options configures a Reconciler.
type Options struct {
	// MinReplicas is the minimum number of readable snapshots required to
	// determine a position baseline. Defaults to 1.
	MinReplicas int
}

// Reconciler orchestrates quarantine, baseline and arbiter. It holds no
// mutable state and is safe for concurrent use.
type Reconciler struct {
	minReplicas int
}

// New returns a Reconciler with the given options.
func New(opts Options) *Reconciler {
	min := opts.MinReplicas
	if min < 1 {
		min = 1
	}
	return &Reconciler{minReplicas: min}
}

// Reconcile merges the given serialized snapshots into one deterministic
// result. It never mutates raws and may be called concurrently.
//
// Error categories are evaluated in a fixed priority order:
//
//  1. insufficient replicas to determine the baseline (fatal, ErrInsufficientReplicas);
//  2. corrupted snapshots (non-fatal, reported in Result.Quarantined);
//  3. irreconcilable objects (non-fatal, reported in Result.Irreconcilable).
//
// The order is fixed because each stage is a precondition for the next:
// the baseline requires a quorum of readable snapshots; quarantine decides
// the participant set; arbitration runs last, over the surviving
// participants only. When ErrInsufficientReplicas is returned, the Result
// is still non-nil and carries any quarantine records collected so far.
func (r *Reconciler) Reconcile(raws [][]byte) (*Result, error) {
	res := &Result{Objects: map[string]map[string]string{}}
	res.Stats.ReplicasInput = len(raws)

	// Stage 2 (quarantine) runs first physically, but its findings only
	// matter after the fatal stage-1 check; the reported priority order is
	// preserved by evaluating ErrInsufficientReplicas before anything else
	// is acted upon.
	var snaps []*Snapshot
	for _, raw := range raws {
		s, err := decodeSnapshot(raw)
		if err != nil {
			res.Quarantined = append(res.Quarantined, QuarantineRecord{
				Kind:    KindCorruptedSnapshot,
				Replica: salvageReplicaID(raw),
				Reason:  err.Error(),
			})
			continue
		}
		snaps = append(snaps, s)
	}
	// Quarantine records are part of the result; order them by content so
	// the result cannot drift with input arrival order.
	sort.Slice(res.Quarantined, func(i, j int) bool {
		a, b := res.Quarantined[i], res.Quarantined[j]
		if a.Replica != b.Replica {
			return a.Replica < b.Replica
		}
		return a.Reason < b.Reason
	})
	res.Stats.ReplicasQuarantine = len(res.Quarantined)
	res.Stats.ReplicasUsed = len(snaps)

	// Stage 1: baseline feasibility.
	if len(snaps) < r.minReplicas {
		return res, fmt.Errorf("%w: need %d, have %d readable",
			ErrInsufficientReplicas, r.minReplicas, len(snaps))
	}

	// Deterministic participant order: content-identical inputs always
	// merge in the same sequence regardless of arrival order.
	sort.Slice(snaps, func(i, j int) bool {
		a, b := snaps[i], snaps[j]
		if a.Replica.ID != b.Replica.ID {
			return a.Replica.ID < b.Replica.ID
		}
		if a.Replica.Priority != b.Replica.Priority {
			return a.Replica.Priority < b.Replica.Priority
		}
		return a.Pos < b.Pos
	})

	res.Baseline = baselinePosition(snaps)

	// Merge: one pass over all values. Cost is linear in the number of
	// input values; candidates are only collected once a real conflict
	// (two differing values for the same property) is observed.
	type propEntry struct {
		common     string // value shared by all replicas so far
		conflicted bool
		candidates []Candidate
	}
	merged := map[string]map[string]*propEntry{}
	for _, s := range snaps {
		for objID, obj := range s.Objects {
			props := merged[objID]
			if props == nil {
				props = map[string]*propEntry{}
				merged[objID] = props
			}
			for prop, vv := range obj.Props {
				res.Stats.ValuesScanned++
				if vv.WrittenAt > res.Baseline {
					// Written after the baseline position: not part of
					// the state being reconciled in this run.
					res.Stats.ValuesFiltered++
					continue
				}
				cand := Candidate{Replica: s.Replica.ID, Priority: s.Replica.Priority, Value: vv.Value}
				e := props[prop]
				if e == nil {
					props[prop] = &propEntry{common: vv.Value, candidates: []Candidate{cand}}
					continue
				}
				if vv.Value != e.common {
					e.conflicted = true
				}
				e.candidates = append(e.candidates, cand)
			}
		}
	}

	// Stage 3: arbitration, only over properties with real conflicts.
	objIDs := sortedKeys(merged)
	for _, objID := range objIDs {
		props := merged[objID]
		out := map[string]string{}
		var rec *ConflictRecord
		irreconcilable := false
		for _, prop := range sortedKeys(props) {
			e := props[prop]
			res.Stats.PropsMerged++
			if !e.conflicted {
				out[prop] = e.common
				continue
			}
			res.Stats.ConflictsFound++
			res.Stats.ArbitrationsRun++
			arb := arbitrate(e.candidates)
			if rec == nil {
				rec = &ConflictRecord{ObjectID: objID}
			}
			if arb.tied != nil {
				irreconcilable = true
				res.Stats.PropsIrreconcilable++
				rec.Unresolved = append(rec.Unresolved, PropertyConflict{
					Prop:       prop,
					Candidates: arb.tied,
					Reason:     arb.tieWhy,
				})
				continue
			}
			out[prop] = arb.winner.Value
			if rec.Resolved == nil {
				rec.Resolved = map[string]Decision{}
			}
			rec.Resolved[prop] = Decision{
				Prop:      prop,
				Winner:    *arb.winner,
				Losers:    arb.losers,
				Rationale: fmt.Sprintf("replica %q wins by smallest priority (%d)", arb.winner.Replica, arb.winner.Priority),
			}
		}
		if irreconcilable {
			res.Irreconcilable = append(res.Irreconcilable, objID)
		} else if len(out) > 0 {
			// Objects whose every write is positioned after the baseline
			// leave no surviving properties and do not exist in the
			// reconciled state.
			res.Objects[objID] = out
		}
		if rec != nil {
			res.Conflicts = append(res.Conflicts, *rec)
		}
	}
	sort.Strings(res.Irreconcilable)
	return res, nil
}

// salvageReplicaID best-effort extracts the replica id from a corrupted
// snapshot for reporting; it never affects arbitration.
func salvageReplicaID(raw []byte) ReplicaID {
	var probe struct {
		Replica struct {
			ID ReplicaID `json:"id"`
		} `json:"replica"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		probe.Replica.ID = ""
	}
	if probe.Replica.ID != "" {
		return probe.Replica.ID
	}
	// The payload is structurally broken; fall back to a textual scan so
	// the quarantine record can still name its origin.
	if m := idPattern.FindSubmatch(raw); m != nil {
		return ReplicaID(m[1])
	}
	return ""
}

var idPattern = regexp.MustCompile(`"id"\s*:\s*"([^"]+)"`)

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
