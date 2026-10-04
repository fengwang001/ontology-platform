// Package decider evaluates the four ordered veto rules D1-D4.
//
// Every function is a pure predicate over facts supplied by the caller; the
// package never touches locks or mutates cluster state. When a copy is being
// moved off its source node, the caller evaluates candidates with the copy
// already conceptually removed (UsedMinusSize/ZoneCopiesMinusOne set, the
// source node simply excluded from the candidate list).
package decider

// Reason names the first vetoing rule.
type Reason int

const (
	// OK means every rule passed.
	OK Reason = iota
	D1Excluded
	D2SameShard
	D3Awareness
	D4Disk
)

// String returns the short rule name used in logs.
func (r Reason) String() string {
	switch r {
	case D1Excluded:
		return "D1"
	case D2SameShard:
		return "D2"
	case D3Awareness:
		return "D3"
	case D4Disk:
		return "D4"
	default:
		return "OK"
	}
}

// Facts are the precomputed facts for one candidate node evaluation.
type Facts struct {
	Excluded           bool  // D1
	HasShardCopy       bool  // D2: node already holds any copy of this shard
	ZoneCopies         int   // D3: copies of this shard in the candidate zone
	ZoneCopiesMinusOne bool  // D3: candidate shares the source zone during a move
	Moving             bool  // true during phase-2 migration (always low watermark)
	PerZone            int   // ceil(c/z)
	Used               int64 // D4: current node usage (candidate never holds the moving copy)
	Total              int64
	Size               int64
	LowPct             int
	HighPct            int
	Primary            bool
}

func zoneCopies(f Facts) int {
	if f.Moving && f.ZoneCopiesMinusOne {
		return f.ZoneCopies - 1
	}
	return f.ZoneCopies
}

func overWater(used, total int64, pct int, size int64) bool {
	return (used+size)*100 > int64(pct)*total
}

// Decide applies D1..D4 in order and returns the first veto (or OK).
// When evals is non-nil each examined rule increments it by one.
func Decide(f Facts, evals *uint64) Reason {
	tick := func() {
		if evals != nil {
			*evals++
		}
	}

	tick()
	if f.Excluded {
		return D1Excluded
	}

	tick()
	if f.HasShardCopy {
		return D2SameShard
	}

	tick()
	if zoneCopies(f)+1 > f.PerZone {
		return D3Awareness
	}

	tick()
	used := f.Used
	pct := f.LowPct
	if f.Primary && !f.Moving {
		pct = f.HighPct
	}
	if overWater(used, f.Total, pct, f.Size) {
		return D4Disk
	}

	return OK
}
