// Package stat computes share statistics and fairness metrics, and tracks
// executed cost per tenant with an injectable clock.
package stat

import (
	"sync"
	"time"
)

// Tracker records executed cost per tenant. The clock is injectable; a nil
// clock means time.Now.
type Tracker struct {
	now  func() time.Time
	mu   sync.Mutex
	exec map[string]int64
	last map[string]time.Time
}

// NewTracker returns a Tracker using the given clock.
func NewTracker(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{now: now, exec: map[string]int64{}, last: map[string]time.Time{}}
}

// Record notes that the tenant executed work of the given cost.
func (t *Tracker) Record(id string, cost int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exec[id] += cost
	t.last[id] = t.now()
}

// Executed returns a copy of the executed cost per tenant.
func (t *Tracker) Executed() map[string]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]int64, len(t.exec))
	for k, v := range t.exec {
		out[k] = v
	}
	return out
}

// LastServed reports when the tenant was last recorded as served.
func (t *Tracker) LastServed(id string) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last[id]
}

// Shares normalizes executed costs into fractions of the total.
func Shares(executed map[string]int64) map[string]float64 {
	var total int64
	for _, v := range executed {
		total += v
	}
	out := map[string]float64{}
	for k, v := range executed {
		if total > 0 {
			out[k] = float64(v) / float64(total)
		}
	}
	return out
}

// Expected normalizes weights into fair-share fractions.
func Expected(weights map[string]float64) map[string]float64 {
	var total float64
	for _, v := range weights {
		total += v
	}
	out := map[string]float64{}
	for k, v := range weights {
		if total > 0 {
			out[k] = v / total
		}
	}
	return out
}

// RelDeviation returns |actual-expected|/expected per tenant.
func RelDeviation(actual, expected map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, e := range expected {
		if e == 0 {
			continue
		}
		d := actual[k] - e
		if d < 0 {
			d = -d
		}
		out[k] = d / e
	}
	return out
}
