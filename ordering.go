package ontology

func scoreEqual(left, right float64) bool {
	return left == right
}

// betterInRank reports whether left comes before right in Snapshot order.
func betterInRank(left, right Element, direction Direction) bool {
	if scoreEqual(left.Score, right.Score) {
		return left.ID < right.ID
	}
	if direction == Desc {
		return left.Score > right.Score
	}
	return left.Score < right.Score
}

// weakerInHeap reports whether left is closer to the heap's root.
// The root is the weakest retained element under betterInRank.
func weakerInHeap(left, right Element, direction Direction) bool {
	if scoreEqual(left.Score, right.Score) {
		return left.ID > right.ID
	}
	if direction == Desc {
		return left.Score < right.Score
	}
	return left.Score > right.Score
}
