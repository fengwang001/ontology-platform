// Package report renders deterministic audit reports for pruning decisions.
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/filter"
)

// Report is one query's audit record: which columns were pruned from rows,
// which predicate references were rejected, and whether the whole query
// was rejected.
type Report struct {
	Rejected bool
	Pruned   []string
	Refs     []filter.Ref
}

// New canonicalizes its inputs: pruned columns are sorted, refs are merged
// by column (each column reported once with all of its paths) and sorted,
// so the report is independent of construction order.
func New(rejected bool, pruned []string, refs []filter.Ref) Report {
	p := append([]string(nil), pruned...)
	sort.Strings(p)
	byCol := map[string]map[string]bool{}
	for _, ref := range refs {
		set, ok := byCol[ref.Column]
		if !ok {
			set = map[string]bool{}
			byCol[ref.Column] = set
		}
		for _, path := range ref.Paths {
			set[path] = true
		}
	}
	cols := make([]string, 0, len(byCol))
	for col := range byCol {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	out := make([]filter.Ref, 0, len(cols))
	for _, col := range cols {
		paths := make([]string, 0, len(byCol[col]))
		for path := range byCol[col] {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		out = append(out, filter.Ref{Column: col, Paths: paths})
	}
	return Report{Rejected: rejected, Pruned: p, Refs: out}
}

// String renders the report in a fixed, byte-deterministic format.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "rejected=%v\n", r.Rejected)
	fmt.Fprintf(&b, "pruned=%s\n", strings.Join(r.Pruned, ","))
	for _, ref := range r.Refs {
		fmt.Fprintf(&b, "ref=%s@%s\n", ref.Column, strings.Join(ref.Paths, ","))
	}
	return b.String()
}
