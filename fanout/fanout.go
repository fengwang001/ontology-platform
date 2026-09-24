// Package fanout queries many shards concurrently with a hard cap on
// in-flight requests and an overall deadline.
package fanout

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"ontology/shard"
)

// Fanout fans a query out to shards. The zero-maxConcurrent is treated as 1.
type Fanout struct {
	maxConcurrent int
	timeout       time.Duration
	inFlight      atomic.Int64
	peak          atomic.Int64 // historical max in-flight requests
}

func New(maxConcurrent int, timeout time.Duration) *Fanout {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Fanout{maxConcurrent: maxConcurrent, timeout: timeout}
}

// Peak returns the historical maximum number of simultaneous requests.
func (f *Fanout) Peak() int64 { return f.peak.Load() }

// Run queries every shard and returns one Result per shard, in input order.
// Shards not started before the deadline get a canceled Result; in-flight
// requests observe ctx cancellation.
func (f *Fanout) Run(ctx context.Context, shards []shard.Shard) []shard.Result {
	if f.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}
	sem := make(chan struct{}, f.maxConcurrent)
	results := make([]shard.Result, len(shards))
	var wg sync.WaitGroup
	for i, s := range shards {
		if err := ctx.Err(); err != nil { // deadline passed: launch nothing new
			results[i] = shard.Result{ShardID: s.ID(), Bound: s.UpperBound(), Err: err}
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = shard.Result{ShardID: s.ID(), Bound: s.UpperBound(), Err: ctx.Err()}
			continue
		}
		wg.Add(1)
		go func(i int, s shard.Shard) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil { // lost the race with the deadline
				results[i] = shard.Result{ShardID: s.ID(), Bound: s.UpperBound(), Err: err}
				return
			}
			f.track()
			results[i] = s.Query(ctx)
			f.inFlight.Add(-1)
		}(i, s)
	}
	wg.Wait()
	return results
}

func (f *Fanout) track() {
	n := f.inFlight.Add(1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			return
		}
	}
}
