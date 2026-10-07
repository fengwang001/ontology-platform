package netcode

// replayUnacked replays still-unacked moves (ascending seq) on top of the
// authoritative base position and returns the reconciled prediction.
//
// Moves whose seq is in rejected (confirmed rejected by the server) are
// skipped: they must not be replayed as if they had succeeded. Every other
// move is re-applied with the shared step rule (step limit + clamp).
//
// The cost is O(len(unacked)); it never touches already-acknowledged
// history, so it does not grow with the total number of past inputs.
func replayUnacked(base int64, unacked []Move, rejected map[int64]struct{}, w, m int64) int64 {
	pos := base
	for _, mv := range unacked {
		if _, ok := rejected[mv.Seq]; ok {
			continue
		}
		pos, _ = applyStep(pos, mv.Delta, w, m)
	}
	return pos
}
