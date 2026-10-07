package reconcile

// baseline is the component that determines the logical position baseline:
// the earliest position among all participating (readable) snapshots. The
// reconciled state corresponds to that position; anything written later is
// excluded from this run.

// baselinePosition returns the minimum position across snapshots.
func baselinePosition(snaps []*Snapshot) Position {
	var min Position
	for i, s := range snaps {
		if i == 0 || s.Pos < min {
			min = s.Pos
		}
	}
	return min
}
