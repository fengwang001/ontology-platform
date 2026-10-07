package demand

// energyHistory is the sliding-window energy ledger.
//
// Key observation: window ends (and therefore window starts, since the
// window length is a multiple of the slip) are always absolute multiples
// of the slip. So it suffices to remember the cumulative consumed energy
// at each slip boundary. Between two accepted reports the power is
// constant, so the cumulative energy at any boundary inside the interval
// is computed by linear interpolation.
//
// Only boundaries within one window length behind the current time are
// ever queried again, so older entries are pruned: memory stays bounded
// by WindowSeconds/SlipSeconds+1 entries and evaluation cost never grows
// with the number of historical reports.
type energyHistory struct {
	slip   int64
	window int64
	// cum maps a slip-boundary timestamp to the cumulative energy (kW*s)
	// consumed up to that timestamp. Energy before the first accepted
	// report is zero; such boundaries are simply absent from the map.
	cum map[int64]float64
}

func newEnergyHistory(window, slip int64) *energyHistory {
	return &energyHistory{
		slip:   slip,
		window: window,
		cum:    make(map[int64]float64, window/slip+1),
	}
}

// cumAt returns the cumulative energy at slip boundary b. Boundaries at
// or before the first accepted report (never recorded) yield zero, which
// matches the rule that the time before the first report has no usage.
func (h *energyHistory) cumAt(b int64) float64 {
	return h.cum[b]
}

// advance records the cumulative energy at every slip boundary in
// (prevT, now], assuming constant interval power, and returns those
// boundary timestamps in ascending order (they are the window ends that
// closed with this report).
func (h *energyHistory) advance(prevT, now int64, cumPrev, power float64) []int64 {
	var ends []int64
	for b := (prevT/h.slip + 1) * h.slip; b <= now; b += h.slip {
		h.cum[b] = cumPrev + power*float64(b-prevT)
		ends = append(ends, b)
	}
	return ends
}

// prune drops boundaries at or before cutoff; they can never be the
// start of a window that is still open or will close in the future.
func (h *energyHistory) prune(cutoff int64) {
	for b := range h.cum {
		if b <= cutoff {
			delete(h.cum, b)
		}
	}
}
