package netcode

// applyStep applies one move to pos under the shared rules:
//   - if |delta| > m the move is rejected: position unchanged, accepted=false;
//   - otherwise pos+delta is clamped into [0, w]; a clamped move still
//     counts as accepted (accepted=true).
//
// It is the single source of truth used by the server (Tick), the client
// (predict on submit) and the reconciler (replay unacked inputs), which is
// what makes server and client results exactly reproducible.
func applyStep(pos, delta, w, m int64) (newPos int64, accepted bool) {
	// Compare without computing abs(delta) so delta == math.MinInt64 is safe.
	if delta > m || delta < -m {
		return pos, false
	}
	next := pos + delta
	switch {
	case next < 0:
		next = 0
	case next > w:
		next = w
	}
	return next, true
}
