// Package row defines a row and its total ordering for keyset pagination.
package row

// Row is one data record: a float64 sort value plus a unique string ID.
type Row struct {
	Score float64
	ID    string
}

// Compare is the total order on rows: Score first, then ID lexicographically.
// It returns -1/0/1. NaN scores compare smaller than every non-NaN score and
// two NaN scores tie on ID; this keeps the relation a strict total order
// given unique IDs.
func Compare(a, b Row) int {
	switch {
	case a.Score < b.Score:
		return -1
	case a.Score > b.Score:
		return 1
	case scoreNaN(a.Score) && !scoreNaN(b.Score):
		return -1
	case !scoreNaN(a.Score) && scoreNaN(b.Score):
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

// Key compares a (score, id) composite key against a row without allocating.
func Key(score float64, id string, r Row) int {
	return Compare(Row{Score: score, ID: id}, r)
}

// Less reports whether a precedes b in the total order.
func Less(a, b Row) bool { return Compare(a, b) < 0 }

func scoreNaN(f float64) bool { return f != f }
