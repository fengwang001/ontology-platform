// Package topk implements a concurrent-safe, fixed-capacity streaming
// Top-K selector over (ID, Score) pairs.
package topk

// Direction selects which end of the score dimension is kept.
// It affects only the score comparison; ties are always broken by
// ascending ID regardless of direction.
type Direction int

const (
	// Desc keeps the K highest scores.
	Desc Direction = iota
	// Asc keeps the K lowest scores.
	Asc
)

// Item is a single ranked element.
type Item struct {
	ID    string
	Score float64
}

// better reports whether a ranks strictly before b under dir.
// Scores compare along dir only; equal scores (including +0.0 vs -0.0,
// which == treats as equal) always break by ascending ID. NaN scores
// never reach this function: they are rejected at Push time.
func better(a, b Item, dir Direction) bool {
	if a.Score != b.Score {
		if dir == Desc {
			return a.Score > b.Score
		}
		return a.Score < b.Score
	}
	return a.ID < b.ID
}
