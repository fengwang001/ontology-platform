// Package stat tracks per-tenant served cost and measures how far the
// actual shares deviate from the weight-proportional fair shares.
package stat

import (
	"math"
	"sync"
)

// Tracker accumulates served cost per tenant. Safe for concurrent use.
type Tracker struct {
	mu     sync.Mutex
	served map[string]float64
	total  float64
}

// New creates an empty tracker.
func New() *Tracker {
	return &Tracker{served: make(map[string]float64)}
}

// Record adds cost to tenant's served total.
func (t *Tracker) Record(tenant string, cost float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.served[tenant] += cost
	t.total += cost
}

// Share returns tenant's fraction of all served cost.
func (t *Tracker) Share(tenant string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.total == 0 {
		return 0
	}
	return t.served[tenant] / t.total
}

// MaxDeviation returns the largest per-tenant relative deviation
// |share - wShare| / wShare against the given weights.
func (t *Tracker) MaxDeviation(weights map[string]float64) float64 {
	wsum := 0.0
	for _, w := range weights {
		wsum += w
	}
	worst := 0.0
	for id, w := range weights {
		want := w / wsum
		if want == 0 {
			continue
		}
		if dev := math.Abs(t.Share(id)-want) / want; dev > worst {
			worst = dev
		}
	}
	return worst
}
