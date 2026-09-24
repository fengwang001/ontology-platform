// Package combine merges successful shard records into five aggregates.
package combine

import (
	"sort"

	"ontology/fanout"
	"ontology/shard"
)

// Kind identifies an aggregate.
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	TopK
)

// Values holds all five aggregates for one fan-out result.
type Values struct {
	Count int64
	Sum   float64
	Min   float64
	Max   float64
	TopK  []shard.Record
	// HasMin reports whether any record existed (empty success is valid).
	HasMin bool
}

// All computes every aggregate over the successful shards. K bounds TopK.
func All(r *fanout.Result, k int) Values {
	var recs []shard.Record
	for _, o := range r.Outcomes {
		if o.Status == fanout.StatusOK {
			recs = append(recs, o.Records...)
		}
	}
	v := Values{TopK: topK(recs, k)}
	for _, rec := range recs {
		v.Count++
		v.Sum += rec.V
		if !v.HasMin || rec.V < v.Min {
			v.Min = rec.V
		}
		if !v.HasMin || rec.V > v.Max {
			v.Max = rec.V
		}
		v.HasMin = true
	}
	return v
}

func topK(recs []shard.Record, k int) []shard.Record {
	sorted := make([]shard.Record, len(recs))
	copy(sorted, recs)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].V != sorted[j].V {
			return sorted[i].V > sorted[j].V
		}
		return sorted[i].ID < sorted[j].ID
	})
	if k < 0 {
		k = 0
	}
	if k >= len(sorted) {
		return sorted
	}
	return sorted[:k]
}

// MissingShardIDs returns the IDs of non-OK shards, sorted for determinism.
func MissingShardIDs(r *fanout.Result) []string {
	var ids []string
	for _, o := range r.Outcomes {
		if o.Status != fanout.StatusOK {
			ids = append(ids, o.ShardID)
		}
	}
	sort.Strings(ids)
	return ids
}

// MissingBoundSum sums the upper bounds of the missing shards.
func MissingBoundSum(r *fanout.Result) float64 {
	b := 0.0
	for _, o := range r.Outcomes {
		if o.Status != fanout.StatusOK {
			b += o.Bound
		}
	}
	return b
}

// AllOK reports whether every shard succeeded.
func AllOK(r *fanout.Result) bool {
	for _, o := range r.Outcomes {
		if o.Status != fanout.StatusOK {
			return false
		}
	}
	return true
}
