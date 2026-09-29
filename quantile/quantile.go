package quantile

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var mergeMu sync.Mutex

var (
	ErrInvalidBudget   = errors.New("quantile: budget must be greater than zero")
	ErrInvalidValue    = errors.New("quantile: value must be a finite float64")
	ErrInvalidQuantile = errors.New("quantile: quantile must be in [0, 1]")
	ErrEmpty           = errors.New("quantile: maintainer has no values")
	ErrValueNotFound   = errors.New("quantile: value to withdraw is not present")
	ErrNilMaintainer   = errors.New("quantile: other maintainer is nil")
	ErrMergeSelf       = errors.New("quantile: cannot merge a maintainer with itself")
)

type Centroid struct {
	Mean  float64
	Count int
	Min   float64
	Max   float64
}

// Maintainer stores exact values and a budget-limited sorted centroid summary.
type Maintainer struct {
	mu        sync.RWMutex
	budget    int
	values    []float64
	centroids []Centroid
}

// New creates a maintainer with a positive maximum number of centroids.
func New(budget int) (*Maintainer, error) {
	if budget <= 0 {
		return nil, ErrInvalidBudget
	}
	return &Maintainer{budget: budget}, nil
}

// Add inserts a finite value and compresses the sorted centroids to the budget.
func (m *Maintainer) Add(value float64) error {
	if !isFinite(value) {
		return ErrInvalidValue
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.values = append(m.values, value)
	m.centroids = append(m.centroids, Centroid{
		Mean:  value,
		Count: 1,
		Min:   value,
		Max:   value,
	})
	sort.SliceStable(m.centroids, func(i, j int) bool {
		return m.centroids[i].Mean < m.centroids[j].Mean
	})
	m.centroids = compressCentroids(m.centroids, m.budget)
	return nil
}

// Withdraw removes one occurrence of value after validating that it exists.
func (m *Maintainer) Withdraw(value float64) error {
	if !isFinite(value) {
		return ErrInvalidValue
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	index := -1
	for i, candidate := range m.values {
		if candidate == value {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrValueNotFound
	}

	m.values = append(m.values[:index], m.values[index+1:]...)
	sort.Float64s(m.values)
	m.centroids = rebuildCentroids(m.values, m.budget)
	return nil
}

// Merge appends a snapshot of other's centroids and values, then compresses.
func (m *Maintainer) Merge(other *Maintainer) error {
	if other == nil {
		return ErrNilMaintainer
	}
	if other == m {
		return ErrMergeSelf
	}

	mergeMu.Lock()
	defer mergeMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	otherValues := append([]float64(nil), other.values...)
	merged := append(append([]Centroid(nil), m.centroids...), other.centroids...)
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Mean < merged[j].Mean
	})
	merged = compressCentroids(merged, m.budget)

	m.values = append(m.values, otherValues...)
	m.centroids = merged
	return nil
}

// Quantile returns the centroid-based linear interpolation for q in [0, 1].
func (m *Maintainer) Quantile(q float64) (float64, error) {
	if !isFinite(q) || q < 0 || q > 1 {
		return 0, ErrInvalidQuantile
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.values) == 0 {
		return 0, ErrEmpty
	}

	target := q * float64(len(m.values)-1)
	cumulativeBefore := 0.0
	previousPosition := 0.0
	for i := range m.centroids {
		centroid := &m.centroids[i]
		position := cumulativeBefore + (float64(centroid.Count)-1)/2
		if target <= position {
			if i == 0 {
				return centroid.Mean, nil
			}
			weight := (target - previousPosition) / (position - previousPosition)
			return m.centroids[i-1].Mean + weight*(centroid.Mean-m.centroids[i-1].Mean), nil
		}
		cumulativeBefore += float64(centroid.Count)
		previousPosition = position
	}
	return m.centroids[len(m.centroids)-1].Mean, nil
}

// ExactQuantile computes the exact linear quantile from the retained values.
func (m *Maintainer) ExactQuantile(q float64) (float64, error) {
	if !isFinite(q) || q < 0 || q > 1 {
		return 0, ErrInvalidQuantile
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.values) == 0 {
		return 0, ErrEmpty
	}

	values := append([]float64(nil), m.values...)
	sort.Float64s(values)
	position := q * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower], nil
	}
	weight := position - float64(lower)
	return values[lower] + weight*(values[upper]-values[lower]), nil
}

// ErrorBound returns a conservative distance bound around an estimate.
func (m *Maintainer) ErrorBound(q float64) (float64, error) {
	if !isFinite(q) || q < 0 || q > 1 {
		return 0, ErrInvalidQuantile
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.values) == 0 {
		return 0, ErrEmpty
	}

	minimum := m.centroids[0].Min
	maximum := m.centroids[0].Max
	for _, centroid := range m.centroids[1:] {
		minimum = math.Min(minimum, centroid.Min)
		maximum = math.Max(maximum, centroid.Max)
	}
	estimate, err := quantileFromCentroids(m.centroids, q, len(m.values))
	if err != nil {
		return 0, err
	}
	return math.Max(math.Abs(estimate-minimum), math.Abs(maximum-estimate)), nil
}

// Count returns the number of retained values, including repeated values.
func (m *Maintainer) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.values)
}

// Snapshot returns a defensive copy of the current ordered centroids.
func (m *Maintainer) Snapshot() []Centroid {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Centroid(nil), m.centroids...)
}

// SelfCheck validates the maintained centroid and exact-value invariants.
func (m *Maintainer) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.budget <= 0 {
		return ErrInvalidBudget
	}
	if len(m.centroids) > m.budget {
		return errors.New("quantile: centroid count exceeds budget")
	}

	total := 0
	previousMean := math.Inf(-1)
	minimum := math.Inf(1)
	maximum := math.Inf(-1)
	for _, centroid := range m.centroids {
		if centroid.Count <= 0 {
			return errors.New("quantile: centroid count must be positive")
		}
		if !isFinite(centroid.Mean) || !isFinite(centroid.Min) || !isFinite(centroid.Max) {
			return errors.New("quantile: centroid contains a non-finite value")
		}
		if centroid.Mean < previousMean {
			return errors.New("quantile: centroids are not sorted by mean")
		}
		if centroid.Min > centroid.Mean || centroid.Mean > centroid.Max {
			return errors.New("quantile: centroid mean is outside its value range")
		}
		previousMean = centroid.Mean
		total += centroid.Count
		minimum = math.Min(minimum, centroid.Min)
		maximum = math.Max(maximum, centroid.Max)
	}
	if total != len(m.values) {
		return errors.New("quantile: centroid count does not match retained values")
	}
	for _, value := range m.values {
		if !isFinite(value) {
			return ErrInvalidValue
		}
		if value < minimum || value > maximum {
			return errors.New("quantile: retained value is outside centroid range")
		}
	}
	return nil
}

func compressCentroids(centroids []Centroid, budget int) []Centroid {
	for len(centroids) > budget {
		index := 0
		bestSum := centroids[0].Count + centroids[1].Count
		for i := 1; i < len(centroids)-1; i++ {
			candidate := centroids[i].Count + centroids[i+1].Count
			if candidate < bestSum {
				bestSum = candidate
				index = i
			}
		}
		centroids[index] = mergeCentroid(centroids[index], centroids[index+1])
		centroids = append(centroids[:index+1], centroids[index+2:]...)
	}
	return centroids
}

func mergeCentroid(left, right Centroid) Centroid {
	total := left.Count + right.Count
	return Centroid{
		Mean:  (left.Mean*float64(left.Count) + right.Mean*float64(right.Count)) / float64(total),
		Count: total,
		Min:   math.Min(left.Min, right.Min),
		Max:   math.Max(left.Max, right.Max),
	}
}

func rebuildCentroids(sortedValues []float64, budget int) []Centroid {
	if len(sortedValues) == 0 {
		return nil
	}

	centroids := make([]Centroid, 0, min(len(sortedValues), budget))
	for _, value := range sortedValues {
		if len(centroids) == 0 || centroids[len(centroids)-1].Mean != value {
			centroids = append(centroids, Centroid{Mean: value, Count: 1, Min: value, Max: value})
			continue
		}
		centroid := &centroids[len(centroids)-1]
		centroid.Count++
	}
	return compressCentroids(centroids, budget)
}

func quantileFromCentroids(centroids []Centroid, q float64, totalCount int) (float64, error) {
	if len(centroids) == 0 || totalCount <= 0 {
		return 0, ErrEmpty
	}
	target := q * float64(totalCount-1)
	cumulativeBefore := 0.0
	previousPosition := 0.0
	previousMean := 0.0
	for i, centroid := range centroids {
		position := cumulativeBefore + (float64(centroid.Count)-1)/2
		if target <= position {
			if i == 0 {
				return centroid.Mean, nil
			}
			weight := (target - previousPosition) / (position - previousPosition)
			return previousMean + weight*(centroid.Mean-previousMean), nil
		}
		cumulativeBefore += float64(centroid.Count)
		previousPosition = position
		previousMean = centroid.Mean
	}
	return centroids[len(centroids)-1].Mean, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
