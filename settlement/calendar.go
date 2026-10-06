package settlement

import "sort"

// calendar maps the (possibly sparse) business-day set to dense indices.
// The day set is fixed for the lifetime of the system; all operations are
// O(log B) in the number of business days.
type calendar struct {
	days []Day // strictly ascending
}

func newCalendar(businessDays []Day) *calendar {
	d := make([]Day, len(businessDays))
	copy(d, businessDays)
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return &calendar{days: d}
}

// contains reports whether d is a business day.
func (c *calendar) contains(d Day) bool {
	_, ok := c.index(d)
	return ok
}

// index returns the dense index of business day d.
func (c *calendar) index(d Day) (int, bool) {
	i := sort.Search(len(c.days), func(i int) bool { return c.days[i] >= d })
	if i < len(c.days) && c.days[i] == d {
		return i, true
	}
	return 0, false
}

// nthOnOrBefore returns the n-th business day on or before d, counting d when
// it is a business day. n must be >= 1. The boolean is false when fewer than n
// business days exist on or before d.
func (c *calendar) nthOnOrBefore(d Day, n int) (Day, bool) {
	i := sort.Search(len(c.days), func(i int) bool { return c.days[i] > d }) - 1
	j := i - n + 1
	if j < 0 {
		return 0, false
	}
	return c.days[j], true
}

// hthAfter returns the h-th business day strictly after d. The boolean is
// false when that day lies beyond the configured calendar.
func (c *calendar) hthAfter(d Day, h int) (Day, bool) {
	i := sort.Search(len(c.days), func(i int) bool { return c.days[i] > d })
	j := i + h - 1
	if j >= len(c.days) {
		return 0, false
	}
	return c.days[j], true
}
