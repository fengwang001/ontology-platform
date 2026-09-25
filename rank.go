package ontology

import (
	"cmp"
	"math"
	"slices"
	"sort"
)

// DefaultCompare returns the built-in row comparator: rows order by
// Value (ascending, or descending when desc is true) and ties break
// by ID ascending in both modes. +0.0 and -0.0 compare equal.
func DefaultCompare(desc bool) func(a, b Row) int {
	return func(a, b Row) int {
		c := cmp.Compare(a.Value, b.Value)
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	}
}

// ComparisonBound returns the comparison-count upper bound for
// ranking n rows in one partition: 10*n*ceil(log2(n+1)). A correct
// sort-based implementation stays well under it; an O(n^2) pairwise
// implementation does not.
func ComparisonBound(n int) int {
	if n <= 0 {
		return 0
	}
	ceil := int(math.Ceil(math.Log2(float64(n) + 1)))
	return 10 * n * ceil
}

// Compute ranks the given rows per partition and returns all three
// ranking columns at once. The input slice and its rows are never
// modified; the returned Summary owns freshly allocated slices.
//
// Rows with a nil partition key or a NaN value are rejected and
// counted in Summary. Ties (equal values, including +0.0/-0.0) share
// the same Rank and DenseRank; their internal order and RowNumber are
// determined by ID ascending, never by input position.
func Compute(rows []Row, opts Options) Summary {
	compare := opts.Compare
	if compare == nil {
		compare = DefaultCompare(opts.Descending)
	}

	groups := make(map[string][]Row)
	var sum Summary
	for _, r := range rows {
		if r.Partition == nil {
			sum.SkippedNilPartition++
			continue
		}
		if math.IsNaN(r.Value) {
			sum.SkippedNaN++
			continue
		}
		groups[*r.Partition] = append(groups[*r.Partition], r)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	total := 0
	for _, g := range groups {
		total += len(g)
	}
	sum.Rows = make([]RankedRow, 0, total)

	for _, k := range keys {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool {
			return compare(g[i], g[j]) < 0
		})
		sum.Rows = append(sum.Rows, rankGroup(k, g)...)
	}
	return sum
}

// rankGroup assigns the three ranking columns to one already-sorted
// partition. Rank restarts at 1 per partition; ties are detected by
// value equality so the rules are identical in both sort directions.
func rankGroup(partition string, sorted []Row) []RankedRow {
	out := make([]RankedRow, len(sorted))
	rank, dense := 0, 0
	for i, r := range sorted {
		if i == 0 || sorted[i-1].Value != r.Value {
			rank = i + 1
			dense++
		}
		out[i] = RankedRow{
			Row:       r,
			Partition: partition,
			RowNumber: i + 1,
			Rank:      rank,
			DenseRank: dense,
		}
	}
	return out
}
