// Package reconcile compares a batch manifest against actual store content
// in one linear pass and reports written/gap/extra segments.
package reconcile

import (
	"sort"
	"strings"

	"ontology/batch"
	"ontology/store"
)

// Report is the reconciliation result for one batch.
type Report struct {
	Committed      bool     // batch has a commit marker
	Written        [][2]int // manifest index intervals present in store
	Gaps           [][2]int // manifest index intervals missing from store
	Extra          []string // store keys tagged for the batch but not in manifest
	InFlightCount  int      // written records of an uncommitted batch
	CommittedCount int      // written records of a committed batch
}

// Run diffs the manifest against the store. It makes exactly one pass over
// store entries plus one pass over manifest indices: O(n) store accesses.
func Run(m *batch.Batch, st *store.Store, committed bool) *Report {
	inManifest := make(map[string]struct{}, m.Len())
	for _, k := range m.Keys {
		inManifest[k] = struct{}{}
	}
	rep := &Report{Committed: committed}
	present := make(map[string]bool, m.Len())
	prefix := m.ID + "\x00"
	st.Visit(func(k, v string) {
		if !strings.HasPrefix(v, prefix) {
			return // belongs to another batch
		}
		if _, ok := inManifest[k]; ok && v == batch.RecordValue(m.ID, k) {
			present[k] = true
		} else {
			rep.Extra = append(rep.Extra, k)
		}
	})
	sort.Strings(rep.Extra)

	// Single index scan merging present/absent runs into intervals.
	for i := 0; i < m.Len(); {
		j := i
		if present[m.Keys[i]] {
			for j < m.Len() && present[m.Keys[j]] {
				j++
			}
			rep.Written = append(rep.Written, [2]int{i, j})
		} else {
			for j < m.Len() && !present[m.Keys[j]] {
				j++
			}
			rep.Gaps = append(rep.Gaps, [2]int{i, j})
		}
		i = j
	}
	written := 0
	for _, iv := range rep.Written {
		written += iv[1] - iv[0]
	}
	if committed {
		rep.CommittedCount = written
	} else {
		rep.InFlightCount = written
	}
	return rep
}
