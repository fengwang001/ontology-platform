// Package window implements a two-window approximate sliding counter.
//
// A counter stores only three integers: the window index k and the hit
// counts of the current and previous windows. The estimated count at time
// now is cur + floor(prev*(W-now%W)/W), weighting the previous window by
// its remaining overlap with the ideal sliding window.
package window

// Counter is a per-(rule, values) approximate sliding-window counter.
// The zero value is not directly usable; construct with NewCounter.
type Counter struct {
	k    int64 // index of the window cur belongs to
	cur  int64 // hits in the current window
	prev int64 // hits in the window before k
}

// NewCounter returns a counter already positioned at the window of now
// with cur = prev = 0.
func NewCounter(now, width int64) Counter {
	return Counter{k: now / width}
}

// roll computes the counter state at time now without mutating the
// counter. Rolling is monotonic and idempotent, so a virtual roll and a
// committed roll always agree.
func (c Counter) roll(now, width int64) (cur, prev int64) {
	k := now / width
	cur, prev = c.cur, c.prev
	switch {
	case k <= c.k:
		// Same window (or a defensive no-op for a non-monotonic now).
	case k == c.k+1:
		prev, cur = cur, 0
	default: // k >= c.k+2: both windows are stale.
		prev, cur = 0, 0
	}
	return cur, prev
}

// Estimate returns cur + floor(prev*(width-now%width)/width) at time now.
// With prev <= 1e9+1 and width <= 1e9 the product stays below 2e18 and
// cannot overflow int64.
func (c Counter) Estimate(now, width int64) int64 {
	cur, prev := c.roll(now, width)
	return cur + prev*(width-now%width)/width
}

// Advance rolls the counter forward to the window of now.
func (c *Counter) Advance(now, width int64) {
	cur, prev := c.roll(now, width)
	c.k = now / width
	c.cur, c.prev = cur, prev
}

// Inc increments cur, capping it at cap (the caller passes L+1).
func (c *Counter) Inc(cap int64) {
	c.cur++
	if c.cur > cap {
		c.cur = cap
	}
}
