// Package report renders the pruning audit report: which columns were
// dropped from result rows, which predicate references were rejected,
// and whether the query was rejected as a whole. Rendering is
// deterministic: identical logical input yields identical bytes
// regardless of construction order.
package report

import (
	"fmt"
	"sort"
	"strings"
)

// Ref is one rejected predicate reference: a column and its path in
// the predicate tree (root "$", e.g. "$.and[1].not").
type Ref struct {
	Column string
	Path   string
}

// Report is the audit record of one query execution.
type Report struct {
	Rejected bool     // the whole query was refused; no rows returned
	Dropped  []string // column names removed from rows, sorted
	Refs     []Ref    // rejected references, sorted by column then path
}

// Normalize sorts and dedupes Dropped and Refs in place, making the
// report independent of the order its fields were assembled in.
func (r *Report) Normalize() {
	sort.Strings(r.Dropped)
	r.Dropped = dedupe(r.Dropped)
	sort.Slice(r.Refs, func(i, j int) bool {
		if r.Refs[i].Column != r.Refs[j].Column {
			return r.Refs[i].Column < r.Refs[j].Column
		}
		return r.Refs[i].Path < r.Refs[j].Path
	})
	r.Refs = dedupeRefs(r.Refs)
}

// String renders the report as a deterministic three-line byte stream.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "rejected: %t\n", r.Rejected)
	fmt.Fprintf(&b, "dropped: %s\n", strings.Join(r.Dropped, ","))
	b.WriteString("refs:")
	for _, ref := range r.Refs {
		fmt.Fprintf(&b, " %s@%s", ref.Column, ref.Path)
	}
	b.WriteString("\n")
	return b.String()
}

func dedupe(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func dedupeRefs(in []Ref) []Ref {
	out := in[:0]
	for i, r := range in {
		if i == 0 || r != in[i-1] {
			out = append(out, r)
		}
	}
	return out
}
