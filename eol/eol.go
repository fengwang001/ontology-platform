// Package eol recognizes \r\n, lone \r and \n line endings across Write cuts.
package eol

// Result is the classification of one input byte.
type Result struct {
	Consumed int  // bytes of input read (0 only for pending \r at EOF before End)
	Line     bool // a line ending was completed by this input
	DropCR   bool // a \r is deleted as part of a \r\n pair
	Pending  bool // a lone \r is held, awaiting the next byte
}

// Stateful recognizes line endings one byte at a time.
type Stateful struct {
	pendingCR bool
}

// Feed classifies b given the preceding stream. Pending \r from a previous
// call is resolved first; Feed never returns more than two bytes of progress.
func (s *Stateful) Feed(b byte) []Result {
	if s.pendingCR {
		s.pendingCR = false
		if b == '\n' {
			return []Result{{Consumed: 1, Line: true, DropCR: true}}
		}
		rs := []Result{{Consumed: 0, Line: true}}
		s.step(b, &rs)
		return rs
	}
	var rs []Result
	s.step(b, &rs)
	return rs
}

func (s *Stateful) step(b byte, rs *[]Result) {
	switch b {
	case '\n':
		*rs = append(*rs, Result{Consumed: 1, Line: true})
	case '\r':
		s.pendingCR = true
		*rs = append(*rs, Result{Consumed: 1, Pending: true})
	default:
		*rs = append(*rs, Result{Consumed: 1})
	}
}

// Pending reports whether a \r is currently held.
func (s *Stateful) Pending() bool { return s.pendingCR }

// End resolves a held \r as a lone line ending.
func (s *Stateful) End() (line bool) {
	if s.pendingCR {
		s.pendingCR, line = false, true
	}
	return line
}
