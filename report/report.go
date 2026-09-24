// Package report builds deterministic audit reports of pruning decisions.
package report

import (
	"fmt"
	"sort"
	"strings"
)

// Ref is one rejected predicate reference: a column plus every path
// at which it occurs in the predicate tree.
type Ref struct {
	Col   string
	Paths []string
}

// Report is the audit record of one query. Insertion order does not
// affect the serialized form: everything is sorted and deduplicated.
type Report struct {
	pruned   map[string]bool
	refs     map[string]map[string]bool
	rejected bool
}

// New returns an empty Report.
func New() *Report {
	return &Report{pruned: map[string]bool{}, refs: map[string]map[string]bool{}}
}

// AddPruned records removed column names.
func (r *Report) AddPruned(cols ...string) {
	for _, c := range cols {
		r.pruned[c] = true
	}
}

// AddRef records one rejected reference (column + path). The same
// column may be added many times; each path is reported once.
func (r *Report) AddRef(col, path string) {
	if r.refs[col] == nil {
		r.refs[col] = map[string]bool{}
	}
	r.refs[col][path] = true
}

// SetRejected marks whether the whole query was rejected.
func (r *Report) SetRejected(v bool) { r.rejected = v }

// Rejected reports whether the whole query was rejected.
func (r *Report) Rejected() bool { return r.rejected }

// Pruned returns the removed column names in lexicographic order.
func (r *Report) Pruned() []string {
	out := make([]string, 0, len(r.pruned))
	for c := range r.pruned {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Refs returns the rejected references sorted by column, each with
// its paths sorted and deduplicated.
func (r *Report) Refs() []Ref {
	out := make([]Ref, 0, len(r.refs))
	for col, paths := range r.refs {
		ps := make([]string, 0, len(paths))
		for p := range paths {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		out = append(out, Ref{Col: col, Paths: ps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Col < out[j].Col })
	return out
}

// String renders the canonical, byte-deterministic form of the report.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "rejected=%t\n", r.rejected)
	fmt.Fprintf(&b, "pruned=%s\n", strings.Join(r.Pruned(), ","))
	for _, ref := range r.Refs() {
		fmt.Fprintf(&b, "ref=%s@%s\n", ref.Col, strings.Join(ref.Paths, ";"))
	}
	return b.String()
}
