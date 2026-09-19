// Package topk implements a bounded, concurrency-safe streaming Top-K
// selector over (ID, Score) pairs.
package topk

// Direction selects which end of the score range is kept. It only affects
// the score dimension; ties are always broken by ascending ID regardless
// of direction.
type Direction int

const (
	// Desc keeps the K elements with the largest scores.
	Desc Direction = iota
	// Asc keeps the K elements with the smallest scores.
	Asc
)

// Element is a single ranked item.
type Element struct {
	ID    string
	Score float64
}

// less reports whether a ranks strictly before b under the composite
// order: direction applies to Score only, and Score ties (including
// +0.0 vs -0.0, which compare equal) are broken by ascending ID.
func less(dir Direction, a, b Element) bool {
	if a.Score != b.Score {
		if dir == Desc {
			return a.Score > b.Score
		}
		return a.Score < b.Score
	}
	return a.ID < b.ID
}
