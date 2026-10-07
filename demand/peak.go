package demand

// PeakRecord is the highest measured demand observed so far.
type PeakRecord struct {
	// DemandKW is the average power over the record window.
	DemandKW float64
	// WindowEnd is the end timestamp of the record window. On ties the
	// earliest window end wins.
	WindowEnd int64
}

// peakTracker remembers the maximum measured window demand.
type peakTracker struct {
	best float64
	end  int64
	ok   bool
}

// record feeds one closed window's measured demand. Strictly greater
// replaces the record, so ties keep the earliest window end.
func (p *peakTracker) record(demand float64, windowEnd int64) {
	if !p.ok || demand > p.best {
		p.best = demand
		p.end = windowEnd
		p.ok = true
	}
}

func (p *peakTracker) get() (PeakRecord, bool) {
	if !p.ok {
		return PeakRecord{}, false
	}
	return PeakRecord{DemandKW: p.best, WindowEnd: p.end}, true
}
