// Package agg aggregates the points contained in one aligned bucket.
package agg

import (
	"errors"

	"ontology/point"
)

// Result holds every aggregate over a bucket.
type Result struct {
	First float64
	Last  float64
	Min   float64
	Max   float64
	Mean  float64
	Count int64
}

// ErrEmpty means aggregation was requested for an empty bucket.
var ErrEmpty = errors.New("agg: empty bucket")

// Aggregate computes First/Last/Min/Max/Mean/Count.
//
// The caller passes points already ordered by (timestamp, arrival) so that
// First/Last and the summation order for Mean are fully deterministic.
// NaN values are assumed to have been rejected earlier; +/-Inf participate
// normally and may make Mean +/-Inf.
func Aggregate(points []point.Point) (Result, error) {
	if len(points) == 0 {
		return Result{}, ErrEmpty
	}
	r := Result{
		First: points[0].Value,
		Last:  points[len(points)-1].Value,
		Min:   points[0].Value,
		Max:   points[0].Value,
		Count: int64(len(points)),
	}
	var sum float64
	for _, p := range points {
		sum += p.Value
		if p.Value < r.Min {
			r.Min = p.Value
		}
		if p.Value > r.Max {
			r.Max = p.Value
		}
	}
	r.Mean = sum / float64(len(points))
	return r, nil
}
