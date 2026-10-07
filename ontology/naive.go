package ontology

import (
	"fmt"
	"sort"
)

// NaiveCrossCheck is the independently implemented whole-stream replay
// verdict for the same (object, time, rule version) triple. It shares no
// data structures or helper functions with adjudicate(): it starts from
// the raw event list every time and walks every record linearly.
type NaiveCrossCheck struct {
	Status          ObjectStatus
	VirtualOrphanAt OrderKey
	HasVirtualPoint bool
	HasMarkedRecord bool
	EventsScanned   int
	Agrees          bool
	Mismatch        string
}

// NaiveReplay replays the entire given stream linearly, independently of
// the per-object index, and adjudicates the object at time T. The input
// slice is copied and sorted so the reference model imposes no
// assumptions on how the indexed path organized records.
func NaiveReplay(events []Event, objectID string, atTime int64, rule RuleVersion) (*NaiveCrossCheck, error) {
	stream := append([]Event(nil), events...)
	sort.SliceStable(stream, func(i, j int) bool {
		if stream[i].Time != stream[j].Time {
			return stream[i].Time < stream[j].Time
		}
		return stream[i].Seq < stream[j].Seq
	})

	cutoff := cutoffKey(atTime)
	horizon := OrderKey{Time: rule.EffectiveFrom, Seq: rule.EffectiveFromSeq}

	// Independent global state rebuilt from zero.
	linkDeclared := map[string]bool{}
	created := map[string]bool{}
	var objectType string
	edges := map[EdgeKey]bool{}
	inCount := map[string]int{}
	everActive := map[string]bool{}
	clearedAt := map[string]OrderKey{}
	hasMarked := false
	scanned := 0

	for _, e := range stream {
		if cutoff.Before(e.Key()) {
			break
		}
		scanned++
		switch e.Kind {
		case EvLinkTypeDeclared:
			linkDeclared[e.TypeID] = true
		case EvObjectCreated:
			created[e.ObjectID] = true
			if e.ObjectID == objectID {
				objectType = e.TypeID
			}
		case EvLinkEstablished, EvLinkRevoked:
			edge := EdgeKey{LinkType: e.TypeID, From: e.ObjectID, To: e.PeerID}
			inWindow := rule.Retroactive || !e.Key().Before(horizon)
			required := false
			for _, rt := range rule.Requirement[objectType] {
				if rt == e.TypeID {
					required = true
				}
			}
			if e.Kind == EvLinkEstablished {
				edges[edge] = true
				if edge.To == objectID && required && inWindow {
					if inCount[e.TypeID] == 0 {
						clearedAt[e.TypeID] = OrderKey{}
					}
					inCount[e.TypeID]++
					everActive[e.TypeID] = true
				}
			} else {
				if edges[edge] {
					delete(edges, edge)
					if edge.To == objectID && required && inWindow {
						inCount[e.TypeID]--
						if inCount[e.TypeID] == 0 {
							clearedAt[e.TypeID] = e.Key()
						}
					}
				}
			}
		case EvOrphanMarked:
			if e.ObjectID == objectID && !hasMarked {
				hasMarked = true
			}
		}
	}

	if !created[objectID] {
		return nil, beforeFirstf("naive replay: object %q not created by %d", objectID, atTime)
	}

	hasVirtual := false
	var virtualAt OrderKey
	if !hasMarked && len(rule.Requirement[objectType]) > 0 {
		allNow := true
		var point OrderKey
		for _, rt := range rule.Requirement[objectType] {
			if inCount[rt] != 0 || !everActive[rt] {
				allNow = false
				break
			}
			at := clearedAt[rt]
			if (point.Time == 0 && point.Seq == 0) || at.After(point) {
				point = at
			}
		}
		if allNow && !(point.Time == 0 && point.Seq == 0) {
			hasVirtual = true
			virtualAt = point
		}
	}

	status := StatusActive
	if hasMarked {
		status = StatusCascadeOrphan
	} else if hasVirtual {
		status = StatusRetroactiveOrphan
	}
	return &NaiveCrossCheck{
		Status:          status,
		VirtualOrphanAt: virtualAt,
		HasVirtualPoint: hasVirtual,
		HasMarkedRecord: hasMarked,
		EventsScanned:   scanned,
	}, nil
}

// runCrossCheck compares the indexed verdict against NaiveReplay and
// records the comparison. Disagreement is reported, never silently
// overwritten; both verdicts remain in the audit trail.
func runCrossCheck(s *snapshot, req DetermineRequest) *NaiveCrossCheck {
	// Resolve the same rule the indexed path used.
	var rule RuleVersion
	if req.Basis.IsHead() {
		rule = *s.headVersion
	} else {
		rule = *s.ruleVersions[req.Basis.VersionID]
	}
	naive, err := NaiveReplay(s.events, req.ObjectID, req.AtTime, rule)
	if err != nil {
		return &NaiveCrossCheck{Agrees: false, Mismatch: err.Error()}
	}
	// A second indexed adjudication serves as the "indexed verdict"
	// input to the comparison, keeping this function self-contained.
	indexed, ierr := adjudicate(s, req.ObjectID, req.AtTime, rule)
	if ierr != nil {
		naive.Agrees = false
		naive.Mismatch = "indexed adjudication error: " + ierr.Error()
		return naive
	}
	naive.Agrees = naive.Status == indexed.Status &&
		naive.HasVirtualPoint == indexed.HasVirtualPoint &&
		naive.HasMarkedRecord == indexed.HasMarkedRecord &&
		(!naive.HasVirtualPoint || naive.VirtualOrphanAt == indexed.VirtualOrphanAt)
	if !naive.Agrees {
		naive.Mismatch = fmt.Sprintf("naive status=%d virtual=%+v marked=%v vs indexed status=%d virtual=%+v marked=%v",
			naive.Status, naive.VirtualOrphanAt, naive.HasMarkedRecord,
			indexed.Status, indexed.VirtualOrphanAt, indexed.HasMarkedRecord)
	}
	return naive
}
