package enrollment

// ledger holds pure functions for duration / year accounting.
type ledger struct {
	cal *Calendar
}

func newLedger(cal *Calendar) *ledger { return &ledger{cal: cal} }

// termOfTick maps a tick to its term index. Versions always start at a term
// boundary (the admission tick is normalized to its term start), so every
// version tick maps back to a term index.
func (l *ledger) termOfTick(tick int64) int {
	t, ok := l.cal.termAt(tick)
	if !ok {
		// Tolerate ticks exactly at the final end boundary.
		if len(l.cal.terms) > 0 && tick == l.cal.terms[len(l.cal.terms)-1].End {
			return len(l.cal.terms) - 1
		}
		return -1
	}
	return t.Index
}

func (l *ledger) suspendedTerms(s *student, curTerm int) int {
	total := 0
	for i := 0; i+1 < len(s.versions); i++ {
		if s.versions[i].Status == StatusSuspended {
			total += s.versions[i].LeaveTerms
		}
	}
	if h := s.headVersion(); h.Status == StatusSuspended && h.LeaveTerms > 0 {
		total += h.LeaveTerms
	}
	return total
}

func (l *ledger) reserveTerms(s *student, curTerm int) int {
	total := 0
	for i := 0; i+1 < len(s.versions); i++ {
		if s.versions[i].Status == StatusReserved {
			total += s.versions[i].LeaveTerms
		}
	}
	if h := s.headVersion(); h.Status == StatusReserved && h.LeaveTerms > 0 {
		total += h.LeaveTerms
	}
	return total
}

// usedYears counts enrolled term indices from the entry term up to and
// including curTerm, excluding every term spent in suspension or reserve.
func (l *ledger) usedYears(s *student, curTerm int) int {
	span := curTerm - s.entryTerm
	if span <= 0 {
		return 0
	}
	leave := 0
	for i, v := range s.versions {
		if v.Status != StatusSuspended && v.Status != StatusReserved {
			continue
		}
		start := l.termOfTick(v.EffectiveAt)
		n := v.LeaveTerms
		if i+1 < len(s.versions) {
			if d := l.termOfTick(s.versions[i+1].EffectiveAt) - start; d < n {
				n = d
			}
		}
		lo, hi := start, start+n
		if lo < s.entryTerm {
			lo = s.entryTerm
		}
		if hi > curTerm {
			hi = curTerm
		}
		if hi > lo {
			leave += hi - lo
		}
	}
	return span - leave
}
