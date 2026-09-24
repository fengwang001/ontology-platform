package combine

import (
	"errors"
	"sort"

	"ontology/shard"
)

var (
	ErrNoShards       = errors.New("no shard attempts")
	ErrAllShardsFailed = errors.New("all shards failed")
)

type Item struct {
	ID    string
	Score int64
}

type Result struct {
	Count        int64
	Sum          int64
	Min          int64
	Max          int64
	TopK         []Item
	Success      []string
	Missing      []string
	MissingUpper int64
	AllOK        bool
	AnyOK        bool
}

func Aggregate(attempts []shard.Attempt, k int) (Result, error) {
	if len(attempts) == 0 {
		return Result{}, ErrNoShards
	}
	ordered := append([]shard.Attempt(nil), attempts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ShardID < ordered[j].ShardID })

	seen := map[string]bool{}
	allOK := true
	var top []Item
	res := Result{}
	minSet, maxSet := false, false
	for _, attempt := range ordered {
		if attempt.Status != shard.StatusOK {
			allOK = false
			res.Missing = append(res.Missing, attempt.ShardID)
			res.MissingUpper += attempt.Response.UpperBound
			continue
		}
		if seen[attempt.ShardID] {
			continue
		}
		seen[attempt.ShardID] = true
		res.AnyOK = true
		res.Success = append(res.Success, attempt.ShardID)
		for _, record := range attempt.Response.Records {
			res.Count++
			if record.Has {
				res.Sum += record.Value
				if !minSet || record.Value < res.Min {
					res.Min, minSet = record.Value, true
				}
				if !maxSet || record.Value > res.Max {
					res.Max, maxSet = record.Value, true
				}
			}
			if record.HasTop {
				top = append(top, Item{ID: record.ID, Score: record.Score})
			}
		}
	}
	if !res.AnyOK {
		return Result{}, ErrAllShardsFailed
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Score != top[j].Score {
			return top[i].Score > top[j].Score
		}
		return top[i].ID < top[j].ID
	})
	if k >= 0 && len(top) > k {
		top = top[:k]
	}
	res.TopK = top
	res.AllOK = allOK
	return res, nil
}
