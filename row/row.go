// Package row defines a data row and the composite sort key
// (score ascending, then unique id ascending) used by keyset pagination.
package row

// Row is one record: a float64 sort value and a unique string id.
type Row struct {
	Score float64
	ID    string
}

// Compare is the total order on composite keys (score, id):
//   -1 if a must come before b, 0 only if Score and ID both equal,
//   +1 if a must come after b. Equal scores break ties by ID.
func Compare(a, b Row) int {
	switch {
	case a.Score < b.Score:
		return -1
	case a.Score > b.Score:
		return 1
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	}
	return 0
}

// Key reports whether row a is strictly before key (score, id).
func Before(a Row, score float64, id string) bool {
	return Compare(a, Row{Score: score, ID: id}) < 0
}

// After reports whether row a is strictly after key (score, id).
func After(a Row, score float64, id string) bool {
	return Compare(a, Row{Score: score, ID: id}) > 0
}

// IDs returns the ids of rows in their current order.
func IDs(rows []Row) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}
