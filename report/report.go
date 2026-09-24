// Package report renders the pruning audit report deterministically.
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/predicate"
)

// ColumnRef aggregates every path at which one column was referenced.
type ColumnRef struct {
	Column string
	Paths  []string
}

// Report is the audit record of one query: which columns were pruned
// from result rows, which predicate references were rejected, and
// whether the whole query was rejected.
type Report struct {
	PrunedColumns []string
	RejectedRefs  []ColumnRef
	Rejected      bool
}

// New builds a Report, normalizing order so the output is independent
// of construction order. Repeated references to the same column keep
// every path (sorted, de-duplicated) but are reported only once.
func New(pruned []string, refs []predicate.Ref, rejected bool) Report {
	cols := append([]string(nil), pruned...)
	sort.Strings(cols)
	cols = dedupeStrings(cols)
	byCol := map[string][]string{}
	for _, r := range refs {
		byCol[r.Column] = append(byCol[r.Column], r.Path)
	}
	names := make([]string, 0, len(byCol))
	for c := range byCol {
		names = append(names, c)
	}
	sort.Strings(names)
	out := make([]ColumnRef, 0, len(names))
	for _, c := range names {
		paths := byCol[c]
		sort.Strings(paths)
		out = append(out, ColumnRef{Column: c, Paths: dedupeStrings(paths)})
	}
	return Report{PrunedColumns: cols, RejectedRefs: out, Rejected: rejected}
}

// String renders the report as deterministic bytes.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "rejected: %v\n", r.Rejected)
	fmt.Fprintf(&b, "pruned-columns: %s\n", strings.Join(r.PrunedColumns, ","))
	b.WriteString("rejected-refs:")
	for _, ref := range r.RejectedRefs {
		fmt.Fprintf(&b, " %s@(%s)", ref.Column, strings.Join(ref.Paths, ","))
	}
	b.WriteString("\n")
	return b.String()
}

func dedupeStrings(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
