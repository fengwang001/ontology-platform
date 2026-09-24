// Package report renders a merged result with confidence annotations,
// the missing-shard list and per-shard statuses.
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/combine"
	"ontology/confidence"
)

// ShardRow is one shard's outcome.
type ShardRow struct {
	ID     string
	Status string
}

// Report is the deterministic, human-readable summary of a merge.
type Report struct {
	Annotations [5]string // Count, Sum, Min, Max, TopK
	Missing     []string
	Shards      []ShardRow
}

func statusName(s combine.Status) string {
	switch s {
	case combine.StatusTimeout:
		return "timeout"
	case combine.StatusCorrupt:
		return "corrupt"
	case combine.StatusError:
		return "error"
	}
	return "ok"
}

// Build assembles the report from a merge result. Output is sorted and
// independent of shard arrival order.
func Build(m combine.Merged) Report {
	var r Report
	for i, agg := range []combine.Agg{combine.AggCount, combine.AggSum,
		combine.AggMin, combine.AggMax, combine.AggTopK} {
		r.Annotations[i] = confidence.Assess(agg, m).Text
	}
	for _, f := range m.Failed {
		r.Missing = append(r.Missing, f.ShardID)
		r.Shards = append(r.Shards, ShardRow{ID: f.ShardID, Status: statusName(f.Status)})
	}
	for _, id := range m.Succeeded {
		r.Shards = append(r.Shards, ShardRow{ID: id, Status: "ok"})
	}
	sort.Strings(r.Missing)
	sort.Slice(r.Shards, func(i, j int) bool { return r.Shards[i].ID < r.Shards[j].ID })
	return r
}

func (r Report) String() string {
	names := []string{"Count", "Sum", "Min", "Max", "TopK"}
	var b strings.Builder
	for i, n := range names {
		fmt.Fprintf(&b, "%s: %s\n", n, r.Annotations[i])
	}
	fmt.Fprintf(&b, "missing: %s\n", strings.Join(r.Missing, ","))
	for _, s := range r.Shards {
		fmt.Fprintf(&b, "shard %q %s\n", s.ID, s.Status)
	}
	return b.String()
}
