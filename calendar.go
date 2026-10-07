package enrollment

import "sort"

// Calendar models an ordered list of half-open term intervals [Start, End).
type Calendar struct {
	terms []Term
}

// Term describes one semester. SubmitDeadline is the configured cutoff tick;
// an application submitted at a tick <= deadline counts as "before the deadline".
type Term struct {
	Index          int
	Start, End     int64
	SubmitDeadline int64
}

func NewCalendar(terms []Term) (*Calendar, error) {
	cp := make([]Term, len(terms))
	copy(cp, terms)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })
	for i, t := range cp {
		if t.Index != i {
			return nil, newErr(ErrInvalidArgument, "term index %d must equal position %d", t.Index, i)
		}
		if t.End <= t.Start {
			return nil, newErr(ErrInvalidArgument, "term %d not half-open positive", i)
		}
		if i > 0 && t.Start != cp[i-1].End {
			return nil, newErr(ErrInvalidArgument, "term %d not contiguous", i)
		}
	}
	return &Calendar{terms: cp}, nil
}

// termAt returns the half-open term containing tick.
func (c *Calendar) termAt(tick int64) (Term, bool) {
	i := sort.Search(len(c.terms), func(i int) bool { return c.terms[i].End > tick })
	if i < len(c.terms) && tick >= c.terms[i].Start {
		return c.terms[i], true
	}
	return Term{}, false
}

func (c *Calendar) term(index int) (Term, bool) {
	if index < 0 || index >= len(c.terms) {
		return Term{}, false
	}
	return c.terms[index], true
}

func (c *Calendar) numTerms() int { return len(c.terms) }
