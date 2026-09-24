// Package combine merges per-shard results into the five aggregations,
// tolerating partial failure, corrupt results and duplicate deliveries.
package combine

import (
	"context"
	"errors"
	"sort"

	"ontology/shard"
)

var (
	ErrNoShards  = errors.New("combine: no shards")
	ErrAllFailed = errors.New("combine: all shards failed")
	ErrCorrupt   = errors.New("combine: claimed count != actual records")
)

// Agg names the five supported aggregations.
type Agg int

const (
	AggCount Agg = iota
	AggSum
	AggMin
	AggMax
	AggTopK
)

// Status classifies a per-shard failure.
type Status int

const (
	StatusOK Status = iota
	StatusTimeout
	StatusCorrupt
	StatusError
)

// Failure records why one shard is missing from the merge.
type Failure struct {
	ShardID string
	Status  Status
	Err     error
}

// Entry is one TopK item.
type Entry struct {
	ID    string
	Value float64
}

// Merged holds the partial merge result. MissingBound is the sum of the
// declared upper bounds of all failed shards.
type Merged struct {
	Count        int
	Sum          float64
	Min, Max     float64
	HasMinMax    bool
	TopK         []Entry
	Succeeded    []string
	Failed       []Failure
	MissingBound float64
}

func classify(err error) Status {
	if errors.Is(err, context.DeadlineExceeded) {
		return StatusTimeout
	}
	return StatusError
}

// Merge combines shard results. Duplicate ShardIDs are counted once
// (first delivery wins). Results may arrive in any order; the output is
// deterministic. k <= 0 yields an empty TopK.
func Merge(results []shard.Result, k int) (Merged, error) {
	if len(results) == 0 {
		return Merged{}, ErrNoShards
	}
	var m Merged
	seen := make(map[string]bool, len(results))
	var entries []Entry
	for _, r := range results {
		if seen[r.ShardID] {
			continue
		}
		seen[r.ShardID] = true
		if r.Err != nil {
			m.Failed = append(m.Failed, Failure{r.ShardID, classify(r.Err), r.Err})
			m.MissingBound += r.Bound
			continue
		}
		if r.Claimed != len(r.Records) {
			m.Failed = append(m.Failed, Failure{r.ShardID, StatusCorrupt, ErrCorrupt})
			m.MissingBound += r.Bound
			continue
		}
		m.Succeeded = append(m.Succeeded, r.ShardID)
		m.Count += len(r.Records)
		for _, rec := range r.Records {
			if !m.HasMinMax || rec.Value < m.Min {
				m.Min = rec.Value
			}
			if !m.HasMinMax || rec.Value > m.Max {
				m.Max = rec.Value
			}
			m.HasMinMax = true
			entries = append(entries, Entry{ID: rec.ID, Value: rec.Value})
		}
	}
	if len(m.Succeeded) == 0 {
		return m, ErrAllFailed
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Value != entries[j].Value {
			return entries[i].Value > entries[j].Value
		}
		return entries[i].ID < entries[j].ID
	})
	for _, e := range entries { // order-independent float sum
		m.Sum += e.Value
	}
	if k > len(entries) {
		k = len(entries)
	}
	if k < 0 {
		k = 0
	}
	m.TopK = entries[:k]
	sort.Strings(m.Succeeded)
	sort.Slice(m.Failed, func(i, j int) bool { return m.Failed[i].ShardID < m.Failed[j].ShardID })
	return m, nil
}
