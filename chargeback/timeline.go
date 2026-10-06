package chargeback

import "sort"

// Money is recorded as immutable point postings keyed by day. Only accepted
// operations ever create postings (immediate claw-backs, manual refunds,
// awards and fees); automatic determinations post nothing here — their money
// effect is computed virtually at read time (see System.autoView).
//
// Prefix sums use compressed-day Fenwick trees rebuilt lazily, giving O(log D)
// reads in the number of distinct days, independent of case/transaction
// counts.
type series struct {
	points map[int]int64
	days   []int
	bit    []int64
	dirty  bool
}

func newSeries() *series { return &series{points: map[int]int64{}} }

func (s *series) add(day int, delta int64) {
	s.points[day] += delta
	s.days = append(s.days, day)
	s.dirty = true
}

func (s *series) rebuild() {
	sort.Ints(s.days)
	uniq := make([]int, 0, len(s.days))
	for _, d := range s.days {
		if len(uniq) == 0 || uniq[len(uniq)-1] != d {
			uniq = append(uniq, d)
		}
	}
	s.days = uniq
	s.bit = make([]int64, len(s.days)+1)
	for i, d := range s.days {
		v := s.points[d]
		for j := i + 1; j < len(s.bit); j += j & -j {
			s.bit[j] += v
		}
	}
	s.dirty = false
}

func (s *series) prefix(day int) int64 {
	if s.dirty {
		s.rebuild()
	}
	idx := sort.SearchInts(s.days, day+1)
	var sum int64
	for i := idx; i > 0; i -= i & -i {
		sum += s.bit[i]
	}
	return sum
}

type timeline struct {
	merchCredit map[string]*series
	merchDelta  map[string]*series
	issuer      *series
	pending     *series
}

func newTimeline() *timeline {
	return &timeline{
		merchCredit: map[string]*series{},
		merchDelta:  map[string]*series{},
		issuer:      newSeries(),
		pending:     newSeries(),
	}
}

func (t *timeline) seriesOf(m map[string]*series, id string) *series {
	s := m[id]
	if s == nil {
		s = newSeries()
		m[id] = s
	}
	return s
}

func (t *timeline) creditMerchant(id string, day int, amount int64) {
	t.seriesOf(t.merchCredit, id).add(day, amount)
}

func (t *timeline) postMerchant(id string, day int, delta int64) {
	t.seriesOf(t.merchDelta, id).add(day, delta)
}

func (t *timeline) postIssuer(day int, delta int64)  { t.issuer.add(day, delta) }
func (t *timeline) postPending(day int, delta int64) { t.pending.add(day, delta) }

func (t *timeline) merchantBalance(id string, day int) int64 {
	var c, d int64
	if x := t.merchCredit[id]; x != nil {
		c = x.prefix(day)
	}
	if x := t.merchDelta[id]; x != nil {
		d = x.prefix(day)
	}
	return c + d
}

func (t *timeline) issuerAt(day int) int64  { return t.issuer.prefix(day) }
func (t *timeline) pendingAt(day int) int64 { return t.pending.prefix(day) }
