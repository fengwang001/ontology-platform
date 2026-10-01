package vegas

// Snapshot is a point-in-time view of the limiter state.
type Snapshot struct {
	L              int64
	N              int64
	NextSeq        int64
	Outstanding    int
	WindowSize     int
	WindowMinRTT   int64
	HasWindowMin   bool
	LastCut        int64
	HasLastCut     bool
	MaxNow         int64
	TimedOutCount  int
	WindowExamined int64
	TokenExamined  int64
}

// State returns a consistent snapshot of the limiter.
func (l *Limiter) State() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	s := Snapshot{
		L:              l.L,
		N:              l.n,
		NextSeq:        l.nextSeq,
		Outstanding:    len(l.tokens),
		WindowSize:     len(l.samples),
		MaxNow:         l.maxNow,
		TimedOutCount:  len(l.timedOut),
		WindowExamined: l.windowExamined,
		TokenExamined:  l.tokenExamined,
	}
	if len(l.mind) > 0 {
		s.HasWindowMin = true
		s.WindowMinRTT = l.samples[l.mind[0]].rtt
	}
	if l.lastCut != nil {
		s.HasLastCut = true
		s.LastCut = *l.lastCut
	}
	return s
}

// Examined returns the cumulative number of window items and token entries the
// amortized data structures have inspected. Both counters are bounded by a
// constant multiple of (admitted tokens + successful samples) overall.
func (l *Limiter) Examined() (window, tokens int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.windowExamined, l.tokenExamined
}
