// Package fanout implements a sharded fan-out query with partial-result merging.
package fanout

import (
	"context"
	"time"
)

// Aggregation is the aggregate requested from every shard.
type Aggregation string

const (
	AggCount Aggregation = "count"
	AggSum   Aggregation = "sum"
	AggMin   Aggregation = "min"
	AggMax   Aggregation = "max"
	AggTopK  Aggregation = "top_k"
)

// ShardSpec is a pre-registered shard with its advertised upper bounds.
// Values returned by the shard must be non-negative integers.
type ShardSpec struct {
	// Name uniquely identifies a shard within one request.
	Name string
	// MaxRows is the upper bound on the number of matching rows.
	MaxRows uint64
	// MaxValue is the upper bound on any single value in the shard.
	MaxValue uint64
}

// Pair is one value with its secondary key for top-k merging.
type Pair struct {
	Key   string
	Value uint64
}

// ShardResult is the successful response of one shard.
type ShardResult struct {
	// Count is the shard-local row count. Used by AggCount.
	Count uint64
	// Sum is the shard-local sum. Used by AggSum.
	Sum uint64
	// Min is the shard-local minimum. Used by AggMin.
	Min uint64
	// Max is the shard-local maximum. Used by AggMax.
	Max uint64
	// Top is the shard-local ranking, best-first. Used by AggTopK.
	Top []Pair
}

// ShardClient performs one aggregate query against one shard.
// The implementation MUST return at most one ShardResult on emit; any later
// emit for the same shard is a duplicate and is discarded (and counted).
// A non-nil error counts as a returned-error failure. A shard that never emits
// before the deadline counts as a timeout.
type ShardClient interface {
	Query(ctx context.Context, shard ShardSpec, agg Aggregation, k int, emit func(ShardResult)) error
}

// Request is a fully validated fan-out request.
type Request struct {
	Shards      []ShardSpec
	Agg         Aggregation
	K           int
	Concurrency int
	Deadline    time.Duration
	Client      ShardClient
}

// Stats reports execution counters alongside the merged answer.
type Stats struct {
	Succeeded int
	// Failed: shard returned an error.
	Failed int
	// TimedOut: shard never produced a usable result before the deadline.
	TimedOut int
	// BoundViolation: shard returned data exceeding its registered bounds.
	BoundViolation int
	// Duplicate: shard emitted more than once; later arrivals discarded.
	Duplicate int
	// PeakConcurrent is the maximum number of simultaneously in-flight requests.
	PeakConcurrent int
}

// Answer is the merged, order-independent result.
type Answer struct {
	Agg Aggregation
	// Exact is true when every shard produced a usable result.
	Exact bool
	// Conclusive is false when no shard succeeded (no conclusion possible).
	Conclusive bool

	// Closed-trust-range endpoints for count/sum; one-sided ranges for
	// min/max. HasLower/HasUpper tell which endpoints are trustworthy.
	Lower    uint64
	Upper    uint64
	HasLower bool
	HasUpper bool

	// Used by top-k.
	Top []RankedPair
}

// RankedPair is a merged ranking entry with certainty marking.
type RankedPair struct {
	Pair
	Certain bool
}
