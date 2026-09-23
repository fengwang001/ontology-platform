package report

import (
	"sort"
	"strings"
)

// Reference identifies one rejected predicate reference and its tree path.
type Reference struct {
	Column string
	Path   string
}

// Report is the deterministic audit record for one filtered query.
type Report struct {
	DroppedColumns []string
	References     []Reference
	Denied         bool
}

// New builds a sorted, deduplicated audit report.
func New(dropped map[string]struct{}, references []Reference, denied bool) Report {
	columns := make([]string, 0, len(dropped))
	for column := range dropped {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	unique := make(map[Reference]struct{}, len(references))
	for _, reference := range references {
		unique[reference] = struct{}{}
	}
	sorted := make([]Reference, 0, len(unique))
	for reference := range unique {
		sorted = append(sorted, reference)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Column != sorted[j].Column {
			return sorted[i].Column < sorted[j].Column
		}
		return sorted[i].Path < sorted[j].Path
	})

	return Report{DroppedColumns: columns, References: sorted, Denied: denied}
}

// Marshal returns a canonical one-line representation with sorted fields.
func (r Report) Marshal() string {
	var builder strings.Builder
	builder.WriteString("denied=")
	if r.Denied {
		builder.WriteString("true")
	} else {
		builder.WriteString("false")
	}
	builder.WriteString(";dropped=")
	builder.WriteString(strings.Join(r.DroppedColumns, ","))
	builder.WriteString(";references=")
	parts := make([]string, 0, len(r.References))
	for _, reference := range r.References {
		parts = append(parts, reference.Column+"@"+reference.Path)
	}
	builder.WriteString(strings.Join(parts, "|"))
	return builder.String()
}
