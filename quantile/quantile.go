// Package quantile provides an approximate quantile maintainer based on a
// bounded list of weighted centroids.
//
// A centroid summarizes a group of values by their arithmetic mean and the
// number of values (weight) it represents. The centroid list is always kept
// in ascending order of mean. The exact set of observed values is retained so
// that retractions can be recomputed deterministically and quantile error can
// be audited against exact answers.
package quantile

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// Centroid summarizes a group of values.
type Centroid struct {
	mean   float64
	weight int64
}

// Mean returns the arithmetic mean of the values represented by the centroid.
func (c Centroid) Mean() float64 { return c.mean }

// Weight returns the number of values represented by the centroid.
func (c Centroid) Weight() int64 { return c.weight }

// Maintainer approximates quantiles of a multiset of values with a bounded
// number of centroids.
type Maintainer struct {
	mu sync.RWMutex

	budget int

	centroids []Centroid
	values    map[float64]int64
	count     int64
}

// Distinguishable rejection reasons.
var (
	ErrInvalidBudget  = errors.New("quantile: budget must be a positive integer")
	ErrEmpty          = errors.New("quantile: quantile queried on an empty maintainer")
	ErrInvalidQ       = errors.New("quantile: quantile point must be within [0, 1]")
	ErrValueNotFound  = errors.New("quantile: cannot retract a value that was never observed")
	ErrInvalidValue   = errors.New("quantile: value must be a finite number")
	ErrNilMaintainer  = errors.New("quantile: cannot merge a nil maintainer")
	ErrIntegrityCheck = errors.New("quantile: internal integrity check failed")
)

// New creates a Maintainer with the given centroid budget.
func New(budget int) (*Maintainer, error) {
	if budget <= 0 {
		return nil, ErrInvalidBudget
	}
	return &Maintainer{
		budget: budget,
		values: make(map[float64]int64),
	}, nil
}

// Add inserts a value and recompresses the centroid list when it exceeds the
// budget.
func (m *Maintainer) Add(value float64) error {
	if !isFinite(value) {
		return ErrInvalidValue
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.insertCentroid(Centroid{mean: value, weight: 1})
	m.compress()
	m.values[value]++
	m.count++
	return nil
}

// Quantile returns the estimated value at the given quantile point q.
func (m *Maintainer) Quantile(q float64) (float64, error) {
	if math.IsNaN(q) || q < 0 || q > 1 {
		return 0, ErrInvalidQ
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.count == 0 {
		return 0, ErrEmpty
	}

	// Target rank in [0, N-1]: q=0 anchors on the first centroid, q=1 on the
	// last one.
	targetRank := q * float64(m.count-1)

	// Anchor rank of the last centroid is N-1.
	var cumulative int64
	for i := range m.centroids {
		anchor := float64(cumulative) + float64(m.centroids[i].weight-1)/2
		if targetRank <= anchor {
			if i == 0 || targetRank == anchor {
				return m.centroids[i].mean, nil
			}
			prev := m.centroids[i-1]
			cur := m.centroids[i]
			prevAnchor := float64(cumulative-cur.weight) + float64(prev.weight-1)/2
			frac := (targetRank - prevAnchor) / (anchor - prevAnchor)
			return prev.mean + frac*(cur.mean-prev.mean), nil
		}
		cumulative += m.centroids[i].weight
	}
	return m.centroids[len(m.centroids)-1].mean, nil
}

// Merge absorbs every centroid of other and recompresses. The other
// maintainer is left unchanged.
func (m *Maintainer) Merge(other *Maintainer) error {
	if other == nil {
		return ErrNilMaintainer
	}
	if m == other {
		return nil
	}

	// Take an atomic snapshot of the other maintainer, then mutate under our
	// own write lock. This keeps other unchanged and avoids lock-ordering
	// hazards.
	other.mu.RLock()
	otherCentroids := make([]Centroid, len(other.centroids))
	copy(otherCentroids, other.centroids)
	otherValues := make(map[float64]int64, len(other.values))
	for value, freq := range other.values {
		otherValues[value] = freq
	}
	otherCount := other.count
	other.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	combined := mergeCentroidLists(m.centroids, otherCentroids)
	combined = compressTo(combined, m.budget)

	for value, freq := range otherValues {
		m.values[value] += freq
	}
	m.count += otherCount
	m.centroids = combined
	return nil
}

// Retract removes one occurrence of a previously observed value and rebuilds
// the centroid list from the remaining exact values. Nothing changes when the
// operation fails.
func (m *Maintainer) Retract(value float64) error {
	if !isFinite(value) {
		return ErrInvalidValue
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.values[value] == 0 {
		return ErrValueNotFound
	}

	// Validate and recompute everything from the exact remaining set before
	// touching any state, so a failure leaves the maintainer bit-for-bit
	// unchanged.
	nextValues := make(map[float64]int64, len(m.values))
	for v, freq := range m.values {
		nextValues[v] = freq
	}
	nextValues[value]--
	if nextValues[value] == 0 {
		delete(nextValues, value)
	}

	nextCount := m.count - 1
	var nextCentroids []Centroid
	if nextCount > 0 {
		values := make([]float64, 0, len(nextValues))
		for v := range nextValues {
			values = append(values, v)
		}
		sort.Float64s(values)
		nextCentroids = make([]Centroid, 0, len(values))
		for _, v := range values {
			nextCentroids = append(nextCentroids, Centroid{mean: v, weight: nextValues[v]})
		}
		nextCentroids = compressTo(nextCentroids, m.budget)
	}

	m.values = nextValues
	m.count = nextCount
	m.centroids = nextCentroids
	return nil
}

// Count returns the total number of values currently represented.
func (m *Maintainer) Count() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.count
}

// Centroids returns a snapshot copy of the centroids in ascending mean order.
func (m *Maintainer) Centroids() []Centroid {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Centroid, len(m.centroids))
	copy(out, m.centroids)
	return out
}

// Check verifies the maintainer invariants and returns a descriptive error
// when any of them is violated.
func (m *Maintainer) Check() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checkLocked()
}

func (m *Maintainer) checkLocked() error {
	if m.budget <= 0 {
		return ErrIntegrityCheck
	}
	if len(m.centroids) > m.budget {
		return ErrIntegrityCheck
	}

	var centroidWeight int64
	var exactCount int64
	var weightedSum float64
	for i, c := range m.centroids {
		if c.weight <= 0 || !isFinite(c.mean) {
			return ErrIntegrityCheck
		}
		if i > 0 && c.mean < m.centroids[i-1].mean {
			return ErrIntegrityCheck
		}
		centroidWeight += c.weight
		weightedSum += c.mean * float64(c.weight)
	}
	for value, freq := range m.values {
		if !isFinite(value) || freq <= 0 {
			return ErrIntegrityCheck
		}
		exactCount += freq
	}
	if centroidWeight != m.count || exactCount != m.count {
		return ErrIntegrityCheck
	}

	var exactSum float64
	for value, freq := range m.values {
		exactSum += value * float64(freq)
	}
	tolerance := 1e-12 * math.Max(1, math.Abs(weightedSum))
	if math.Abs(weightedSum-exactSum) > tolerance {
		return ErrIntegrityCheck
	}
	return nil
}

// insertCentroid inserts a singleton centroid while preserving ascending mean
// order. Equal means stay as distinct adjacent centroids; compression merges
// them only when the budget demands it.
func (m *Maintainer) insertCentroid(c Centroid) {
	pos := sort.Search(len(m.centroids), func(i int) bool {
		return m.centroids[i].mean >= c.mean
	})
	m.centroids = append(m.centroids, Centroid{})
	copy(m.centroids[pos+1:], m.centroids[pos:])
	m.centroids[pos] = c
}

// compress repeatedly merges the adjacent pair with the smallest weight sum
// until the centroid list fits the budget. Ties resolve to the leftmost pair.
func (m *Maintainer) compress() {
	m.centroids = compressTo(m.centroids, m.budget)
}

func compressTo(centroids []Centroid, budget int) []Centroid {
	for len(centroids) > budget {
		best := 0
		bestSum := centroids[0].weight + centroids[1].weight
		for i := 1; i+1 < len(centroids); i++ {
			sum := centroids[i].weight + centroids[i+1].weight
			if sum < bestSum {
				bestSum = sum
				best = i
			}
		}
		merged := mergePair(centroids[best], centroids[best+1])
		centroids[best] = merged
		centroids = append(centroids[:best+1], centroids[best+2:]...)
	}
	return centroids
}

func mergePair(a, b Centroid) Centroid {
	weight := a.weight + b.weight
	mean := (a.mean*float64(a.weight) + b.mean*float64(b.weight)) / float64(weight)
	return Centroid{mean: mean, weight: weight}
}

// mergeCentroidLists merges two ascending centroid lists, combining entries
// with equal means so that equal-valued singleton centroids coalesce.
func mergeCentroidLists(a, b []Centroid) []Centroid {
	out := make([]Centroid, 0, len(a)+len(b))
	i, j := 0, 0
	flush := func(c Centroid) {
		if n := len(out); n > 0 && out[n-1].mean == c.mean {
			out[n-1] = mergePair(out[n-1], c)
		} else {
			out = append(out, c)
		}
	}
	for i < len(a) && j < len(b) {
		if a[i].mean <= b[j].mean {
			flush(a[i])
			i++
		} else {
			flush(b[j])
			j++
		}
	}
	for ; i < len(a); i++ {
		flush(a[i])
	}
	for ; j < len(b); j++ {
		flush(b[j])
	}
	return out
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
