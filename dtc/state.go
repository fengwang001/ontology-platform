package dtc

// dtcState is the persistent lifecycle record of one DTC. It is created
// on the first failed judgment and erased on auto-clear or tester
// clear; it carries no per-cycle data (that lives in cycleEntry), so
// its memory footprint is O(1) regardless of history length.
type dtcState struct {
	severity int

	pending   bool
	confirmed bool
	healed    bool // confirmation healed; kept as history until auto-clear

	occurrences      int
	consecFailCycles int
	faultFreeWarmups int
}

// onFailJudgment applies a debounced failure that occurred in the
// current cycle. firstOfCycle reports whether this is the first failed
// judgment of the cycle for this DTC. It returns true when the DTC
// newly enters the pending state (freeze frame capture point).
func (s *dtcState) onFailJudgment(firstOfCycle bool) (enteredPending bool) {
	if firstOfCycle {
		s.occurrences++
	}
	if !s.pending {
		s.pending = true
		if s.healed {
			// A healed DTC that fails again re-enters pending and
			// restarts its consecutive-failed-cycle count from zero.
			s.healed = false
			s.consecFailCycles = 0
		}
		return true
	}
	return false
}

// settleFailedCycle settles a cycle in which the DTC was judged failed
// at least once. Called at ignition off.
func (s *dtcState) settleFailedCycle(cfg Config) {
	s.faultFreeWarmups = 0
	s.consecFailCycles++
	if s.consecFailCycles >= cfg.ConfirmCycles {
		s.confirmed = true // pending is kept
	}
}

// settlePassedCycle settles a cycle in which monitoring completed (at
// least one passed judgment) without any failure. warmup tells whether
// the cycle qualified as a warm-up cycle. It returns true when the DTC
// must be fully erased (auto-clear).
func (s *dtcState) settlePassedCycle(cfg Config, warmup bool) (autoCleared bool) {
	s.consecFailCycles = 0
	if !warmup || (!s.confirmed && !s.healed) {
		return false
	}
	s.faultFreeWarmups++
	if s.confirmed && s.faultFreeWarmups >= cfg.HealWarmupCycles {
		// Heal: confirmation and pending are cleared, the DTC is
		// kept as history and its fault-free warm-up counter keeps
		// running towards auto-clear.
		s.confirmed = false
		s.pending = false
		s.healed = true
	}
	if s.healed && s.faultFreeWarmups >= cfg.AutoClearWarmups {
		return true
	}
	return false
}

// snapshot builds the query view of the DTC. hasFF reports freeze frame
// ownership, distance is the odometer distance since the clear baseline.
func (s *dtcState) snapshot(j Judgment, hasFF bool, distance int64) Snapshot {
	return Snapshot{
		Pending:            s.pending,
		Confirmed:          s.confirmed,
		Healed:             s.healed,
		Judgment:           j,
		Occurrences:        s.occurrences,
		ConsecFailCycles:   s.consecFailCycles,
		FaultFreeWarmups:   s.faultFreeWarmups,
		HasFreezeFrame:     hasFF,
		DistanceSinceClear: distance,
	}
}
