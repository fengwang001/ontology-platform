// Package trace records provenance: which layer produced each final value
// and which layers it shadowed, alongside the expanded value.
package trace

import (
	"fmt"
	"sort"
	"strings"

	"ontology/merge"
)

// Record is the provenance of one final key: winning layer with its raw
// value, shadowed layers from low to high priority, and the expanded value.
type Record struct {
	Key        string
	Final      merge.Override
	Overridden []merge.Override
	Expanded   string
}

// Build derives sorted provenance records from a merge result and the
// expanded values. Raw values are those captured before expansion.
func Build(res *merge.Result, expanded map[string]string) []Record {
	keys := make([]string, 0, len(res.Entries))
	for k := range res.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	recs := make([]Record, 0, len(keys))
	for _, k := range keys {
		m := res.Entries[k]
		recs = append(recs, Record{
			Key:        k,
			Final:      merge.Override{Layer: m.Layer, Value: m.Value},
			Overridden: m.Overridden,
			Expanded:   expanded[k],
		})
	}
	return recs
}

// Report renders records deterministically, one line per key, sorted.
func Report(recs []Record) string {
	var b strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&b, "key=%s final=%s:%q", r.Key, r.Final.Layer, r.Final.Value)
		b.WriteString(" overridden=")
		parts := make([]string, 0, len(r.Overridden))
		for _, o := range r.Overridden {
			parts = append(parts, fmt.Sprintf("%s:%q", o.Layer, o.Value))
		}
		b.WriteString("[" + strings.Join(parts, ",") + "]")
		fmt.Fprintf(&b, " expanded=%q\n", r.Expanded)
	}
	return b.String()
}
