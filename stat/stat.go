// Package stat tracks executed cost per tenant and measures fairness.
package stat

import (
	"math"
	"sync"
)

// Counter accumulates executed cost and task count per tenant.
// It is safe for concurrent use.
type Counter struct {
	mu     sync.Mutex
	cost   map[string]float64
	counts map[string]uint64
}

// New creates an empty Counter.
func New() *Counter {
	return &Counter{cost: make(map[string]float64), counts: make(map[string]uint64)}
}

// Add records one executed task for a tenant.
func (c *Counter) Add(tenant string, cost float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cost[tenant] += cost
	c.counts[tenant]++
}

// Shares returns each tenant's fraction of the total executed cost.
func (c *Counter) Shares() map[string]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total float64
	for _, v := range c.cost {
		total += v
	}
	out := make(map[string]float64, len(c.cost))
	if total == 0 {
		return out
	}
	for k, v := range c.cost {
		out[k] = v / total
	}
	return out
}

// Counts returns each tenant's executed task count.
func (c *Counter) Counts() map[string]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]uint64, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

// Deviations returns, per tenant, the relative deviation between the
// actual executed-cost share and the weight share:
// (actual - expected) / expected.
func Deviations(shares, weights map[string]float64) map[string]float64 {
	var wsum float64
	for _, w := range weights {
		wsum += w
	}
	out := make(map[string]float64, len(weights))
	for id, w := range weights {
		expected := w / wsum
		out[id] = (shares[id] - expected) / expected
	}
	return out
}

// MaxAbsDeviation returns the tenant with the largest absolute
// relative deviation and that deviation.
func MaxAbsDeviation(shares, weights map[string]float64) (string, float64) {
	var maxID string
	var maxDev float64
	for id, d := range Deviations(shares, weights) {
		if math.Abs(d) > math.Abs(maxDev) {
			maxID, maxDev = id, d
		}
	}
	return maxID, maxDev
}
