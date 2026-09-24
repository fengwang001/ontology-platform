// Package report builds an audit report of pruning and rejection decisions.
package report

import (
	"sort"
	"strings"
)

// ColumnRef names a rejected column once, together with every path at
// which the predicate referenced it.
type ColumnRef struct {
	Col   string
	Paths []string
}

// Report records which columns were pruned from result rows, which
// predicate references were rejected, and whether the query as a whole
// was rejected. All output is canonical: sorted and independent of the
// order in which entries were recorded.
type Report struct {
	dropped map[string]bool
	refs    map[string]map[string]bool // column -> set of paths
	// RejectedQuery is true when the whole query was refused.
	RejectedQuery bool
}

// New returns an empty Report.
func New() *Report {
	return &Report{dropped: map[string]bool{}, refs: map[string]map[string]bool{}}
}

// Drop records columns pruned from result rows.
func (r *Report) Drop(cols ...string) {
	for _, c := range cols {
		r.dropped[c] = true
	}
}

// Reject records invisible-column references; repeated columns merge and
// their paths accumulate.
func (r *Report) Reject(col string, paths ...string) {
	set, ok := r.refs[col]
	if !ok {
		set = map[string]bool{}
		r.refs[col] = set
	}
	for _, p := range paths {
		set[p] = true
	}
}

// Dropped returns the pruned column names in lexicographic order.
func (r *Report) Dropped() []string {
	out := make([]string, 0, len(r.dropped))
	for c := range r.dropped {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Refs returns each rejected column once, in lexicographic order, with all
// of its paths sorted.
func (r *Report) Refs() []ColumnRef {
	cols := make([]string, 0, len(r.refs))
	for c := range r.refs {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	out := make([]ColumnRef, 0, len(cols))
	for _, c := range cols {
		paths := make([]string, 0, len(r.refs[c]))
		for p := range r.refs[c] {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		out = append(out, ColumnRef{Col: c, Paths: paths})
	}
	return out
}

// Canonical renders the report as deterministic bytes: identical inputs
// produce identical output regardless of recording order.
func (r *Report) Canonical() string {
	var b strings.Builder
	if r.RejectedQuery {
		b.WriteString("rejected: yes\n")
	} else {
		b.WriteString("rejected: no\n")
	}
	b.WriteString("dropped: " + strings.Join(r.Dropped(), ",") + "\n")
	refs := make([]string, 0, len(r.refs))
	for _, cr := range r.Refs() {
		refs = append(refs, cr.Col+"@"+strings.Join(cr.Paths, "|"))
	}
	b.WriteString("refs: " + strings.Join(refs, ",") + "\n")
	return b.String()
}
