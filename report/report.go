// Package report renders merged values, confidence and per-shard status.
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/combine"
	"ontology/confidence"
	"ontology/fanout"
)

// Report is the deterministic textual result of a fan-out query.
type Report struct {
	Values   combine.Values
	Labels   map[combine.Kind]confidence.Label
	Missing  []string
	Statuses []StatusLine
}

// StatusLine is one per-shard status row.
type StatusLine struct {
	ShardID string
	Status  fanout.Status
}

// Build assembles a report from a fan-out result.
func Build(r *fanout.Result, k int) Report {
	v := combine.All(r, k)
	labels := confidence.Assess(r, v)
	statuses := make([]StatusLine, 0, len(r.Outcomes))
	for _, o := range r.Outcomes {
		statuses = append(statuses, StatusLine{ShardID: o.ShardID, Status: o.Status})
	}
	sort.SliceStable(statuses, func(i, j int) bool {
		return statuses[i].ShardID < statuses[j].ShardID
	})
	return Report{
		Values:   v,
		Labels:   labels,
		Missing:  combine.MissingShardIDs(r),
		Statuses: statuses,
	}
}

// String renders the report. Output is byte-stable across arrival orders.
func (rep Report) String() string {
	var b strings.Builder
	order := []combine.Kind{combine.Count, combine.Sum, combine.Min, combine.Max, combine.TopK}
	for _, knd := range order {
		b.WriteString(rep.Labels[knd].Text)
		b.WriteByte('\n')
	}
	b.WriteString("missing:")
	if len(rep.Missing) == 0 {
		b.WriteString(" -")
	}
	for _, id := range rep.Missing {
		b.WriteByte(' ')
		b.WriteString(displayID(id))
	}
	b.WriteByte('\n')
	for _, s := range rep.Statuses {
		fmt.Fprintf(&b, "shard %s: %s\n", displayID(s.ShardID), s.Status)
	}
	return b.String()
}

// TopKText renders the guaranteed TopK prefix entries.
func (rep Report) TopKText() string {
	p := rep.Labels[combine.TopK].Prefix
	if p > len(rep.Values.TopK) {
		p = len(rep.Values.TopK)
	}
	var parts []string
	for _, rec := range rep.Values.TopK[:p] {
		parts = append(parts, fmt.Sprintf("%s=%g", displayID(rec.ID), rec.V))
	}
	return strings.Join(parts, ",")
}

func displayID(id string) string {
	if id == "" {
		return "<empty>"
	}
	return id
}
