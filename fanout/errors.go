package fanout

import "fmt"

// RejectKind enumerates the distinguishable reasons a request is rejected
// before any shard request is issued.
type RejectKind string

const (
	// RejectEmptyShards: shard list is empty.
	RejectEmptyShards RejectKind = "empty_shards"
	// RejectDuplicateShardName: two or more shards share a Name.
	RejectDuplicateShardName RejectKind = "duplicate_shard_name"
	// RejectInvalidK: K is not a positive integer for a top-k request.
	RejectInvalidK RejectKind = "invalid_k"
	// RejectInvalidConcurrency: concurrency limit is not positive.
	RejectInvalidConcurrency RejectKind = "invalid_concurrency"
	// RejectInvalidDeadline: deadline duration is not positive.
	RejectInvalidDeadline RejectKind = "invalid_deadline"
	// RejectUnknownAggregation: aggregation is not one of the supported five.
	RejectUnknownAggregation RejectKind = "unknown_aggregation"
	// RejectNilClient: no ShardClient was supplied.
	RejectNilClient RejectKind = "nil_client"
	// RejectInvalidBound: a registered upper bound is invalid (negative).
	RejectInvalidBound RejectKind = "invalid_bound"
)

// RejectError is returned for a request that must be refused wholesale,
// before any shard is contacted.
type RejectError struct {
	Kind RejectKind
	Msg  string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("fanout: request rejected (%s): %s", e.Kind, e.Msg)
}

func reject(kind RejectKind, format string, args ...any) error {
	return &RejectError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// validate performs all pre-flight checks. It never mutates the request.
func validate(req Request) error {
	if len(req.Shards) == 0 {
		return reject(RejectEmptyShards, "shard list is empty")
	}
	seen := make(map[string]struct{}, len(req.Shards))
	for _, shard := range req.Shards {
		if _, ok := seen[shard.Name]; ok {
			return reject(RejectDuplicateShardName, "duplicate shard name %q", shard.Name)
		}
		seen[shard.Name] = struct{}{}
	}
	switch req.Agg {
	case AggCount, AggSum, AggMin, AggMax, AggTopK:
	default:
		return reject(RejectUnknownAggregation, "unknown aggregation %q", req.Agg)
	}
	if req.Agg == AggTopK && req.K <= 0 {
		return reject(RejectInvalidK, "K must be positive, got %d", req.K)
	}
	if req.Concurrency <= 0 {
		return reject(RejectInvalidConcurrency, "concurrency must be positive, got %d", req.Concurrency)
	}
	if req.Deadline <= 0 {
		return reject(RejectInvalidDeadline, "deadline must be positive, got %s", req.Deadline)
	}
	if req.Client == nil {
		return reject(RejectNilClient, "client is nil")
	}
	return nil
}
