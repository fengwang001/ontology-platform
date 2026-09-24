// Package report renders the auditable trimming report.
package report

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ontology/filter"
	"ontology/policy"
)

// Ref is an audited rejected predicate reference.
type Ref struct {
	Column string
	Path   []int
}

// Report is the deterministic audit record of one query.
type Report struct {
	Role             string
	RemovedColumns   []string
	RejectedRefs     []Ref
	QueryRejected    bool
	NodesVisited     int
	ProjectionCopies int
}

// Build constructs the report. Removed columns and rejected references are
// sorted so construction order cannot affect the result.
func Build(pol *policy.Policy, role string, refs []filter.Ref, rejected bool,
	visited, copies int) Report {
	dedup := map[string]struct{}{}
	out := Report{
		Role:             role,
		RemovedColumns:   append([]string(nil), pol.HiddenSet(role)...),
		RejectedRefs:     make([]Ref, 0, len(refs)),
		QueryRejected:    rejected,
		NodesVisited:     visited,
		ProjectionCopies: copies,
	}
	for _, r := range refs {
		dedup[r.Column] = struct{}{}
		out.RejectedRefs = append(out.RejectedRefs, Ref{
			Column: r.Column,
			Path:   append([]int(nil), r.Path...),
		})
	}
	sort.Slice(out.RejectedRefs, func(i, j int) bool {
		pi, pj := pathKey(out.RejectedRefs[i].Path), pathKey(out.RejectedRefs[j].Path)
		if pi != pj {
			return pi < pj
		}
		return out.RejectedRefs[i].Column < out.RejectedRefs[j].Column
	})
	return out
}

// PathString renders a path like [0,1].
func PathString(p []int) string {
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = strconv.Itoa(v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func pathKey(p []int) string { return PathString(p) }

// String renders the report with stable, order-independent content.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "role=%s rejected=%t removed=[%s]\n",
		r.Role, r.QueryRejected, strings.Join(r.RemovedColumns, ","))
	fmt.Fprintf(&b, "nodes=%d copies=%d\n", r.NodesVisited, r.ProjectionCopies)
	if len(r.RejectedRefs) == 0 {
		b.WriteString("references=none\n")
		return b.String()
	}
	seen := map[string]struct{}{}
	lines := make([]string, 0, len(r.RejectedRefs))
	for _, ref := range r.RejectedRefs {
		key := ref.Column + "@" + PathString(ref.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		lines = append(lines, key)
	}
	sort.Strings(lines)
	fmt.Fprintf(&b, "references=%s\n", strings.Join(lines, ";"))
	return b.String()
}

// RejectedColumns returns the sorted unique columns among rejected references.
func (r Report) RejectedColumns() []string {
	seen := map[string]struct{}{}
	for _, ref := range r.RejectedRefs {
		seen[ref.Column] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for col := range seen {
		out = append(out, col)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
