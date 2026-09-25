package ontology

import "math"
import "sort"

// rankedItem is the per-partition working copy of an accepted row.
type rankedItem struct {
	value float64
	id    string
}

// validateAndGroup rejects nil-partition and NaN rows, counts them, and
// groups the remaining rows by partition key. Every item is copied, so
// later work cannot mutate the caller's input.
func validateAndGroup(rows []InputRow) (map[string][]rankedItem, Stats) {
	groups := make(map[string][]rankedItem)
	var stats Stats
	for _, row := range rows {
		if row.Partition == nil {
			stats.SkippedNilPartition++
			continue
		}
		if math.IsNaN(row.Value) {
			stats.SkippedNaN++
			continue
		}
		key := *row.Partition
		groups[key] = append(groups[key], rankedItem{value: row.Value, id: row.ID})
	}
	return groups, stats
}

// sortedPartitions returns partition keys in lexicographic order.
func sortedPartitions(groups map[string][]rankedItem) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
