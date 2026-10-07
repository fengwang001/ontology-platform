package reconcile

import (
	"fmt"
	"sort"
)

// arbiter is the component that adjudicates value conflicts for the same
// object instance across replicas, using one fixed rule: the candidate
// from the replica with the numerically smallest Priority wins. If the
// best Priority is claimed by two or more distinct values, the rule has no
// finer tie-break and the property is irreconcilable.

// arbitration is the outcome for one property of one object.
type arbitration struct {
	winner *Candidate
	losers []Candidate
	tied   []Candidate // non-nil when irreconcilable
	tieWhy string
}

// arbitrate applies the fixed priority rule to candidates of one property.
//
// The rule is a pure function of the candidates: it does not depend on how
// many replicas participate, nor on the order they arrived in. Candidates
// are sorted by (priority, replica id, value) up front so the outcome is
// fully deterministic.
func arbitrate(cands []Candidate) arbitration {
	sorted := make([]Candidate, len(cands))
	copy(sorted, cands)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.Replica != b.Replica {
			return a.Replica < b.Replica
		}
		return a.Value < b.Value
	})

	best := sorted[0].Priority
	var top []Candidate
	for _, c := range sorted {
		if c.Priority == best {
			top = append(top, c)
		}
	}

	// Distinct values among the best-priority candidates.
	seen := map[string]bool{}
	var distinct []Candidate
	for _, c := range top {
		if !seen[c.Value] {
			seen[c.Value] = true
			distinct = append(distinct, c)
		}
	}

	if len(distinct) > 1 {
		// The comparable identifiers themselves conflict: the fixed rule
		// has no finer tie-break, and we must not fall back to arrival
		// order or any other content-unrelated criterion.
		return arbitration{
			tied:   distinct,
			tieWhy: fmt.Sprintf("%d distinct values share the best priority %d", len(distinct), best),
		}
	}

	winner := distinct[0]
	var losers []Candidate
	for _, c := range sorted {
		if c.Value != winner.Value || c.Replica != winner.Replica {
			losers = append(losers, c)
		}
	}
	return arbitration{winner: &winner, losers: losers}
}
