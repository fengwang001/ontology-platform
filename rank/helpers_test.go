package rank_test

import (
	"testing"

	"ontology/rank"
)

// strPtr returns a pointer to s, for building partition keys.
func strPtr(s string) *string { return &s }

// makeRows builds rows in one partition; ids and values must have the
// same length.
func makeRows(key string, ids []string, values []float64) []rank.Row {
	rows := make([]rank.Row, len(ids))
	for i := range ids {
		rows[i] = rank.Row{Partition: strPtr(key), Value: values[i], ID: ids[i]}
	}
	return rows
}

// onlyPartition returns the single partition of res or fails the test.
func onlyPartition(t *testing.T, res rank.Result) rank.PartitionResult {
	t.Helper()
	if res.Skipped != 0 {
		t.Fatalf("expected no skipped rows, got %d", res.Skipped)
	}
	if len(res.Partitions) != 1 {
		t.Fatalf("expected 1 partition, got %d", len(res.Partitions))
	}
	return res.Partitions[0]
}

// triples extracts the (RowNumber, Rank, DenseRank) columns in order.
func triples(p rank.PartitionResult) [][3]int {
	out := make([][3]int, len(p.Rows))
	for i, r := range p.Rows {
		out[i] = [3]int{r.RowNumber, r.Rank, r.DenseRank}
	}
	return out
}

// ids extracts the row IDs in output order.
func ids(p rank.PartitionResult) []string {
	out := make([]string, len(p.Rows))
	for i, r := range p.Rows {
		out[i] = r.Row.ID
	}
	return out
}

// equalStrings reports whether two string slices are identical.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalTriples reports whether two triple slices are identical.
func equalTriples(a, b [][3]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
