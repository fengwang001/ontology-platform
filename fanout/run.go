package fanout

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"ontology/shard"
)

// outcome is a worker's fully local result, handed back by channel so the
// fan-out state is never shared between goroutines.
type outcome struct {
	index int
	state ShardState
}

// Run fans the query out to shards. At most limit requests are in flight at
// any time; the whole run is bounded by d (d <= 0 means no extra deadline).
// Partial failures are returned per shard. It returns shard.ErrNoResults
// only when there are no shards or every shard failed.
func Run(ctx context.Context, shards []shard.Shard, limit int, d time.Duration) (*Result, error) {
	if len(shards) == 0 {
		return &Result{}, shard.ErrNoResults
	}
	if limit < 1 {
		limit = 1
	}
	if d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	results := make(chan outcome, len(shards))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	inflight, peak, started := 0, 0, 0

	for i, sh := range shards {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results <- outcome{index: i, state: ShardState{
				ID: sh.ID(), Status: shard.StatusTimeout, Err: shard.ErrTimeout,
			}}
			continue
		}
		mu.Lock()
		inflight++
		started++
		if inflight > peak {
			peak = inflight
		}
		mu.Unlock()
		wg.Add(1)
		go func(index int, sh shard.Shard) {
			defer wg.Done()
			st := fetchOne(ctx, sh)
			st.ID = sh.ID()
			results <- outcome{index: index, state: st}
			mu.Lock()
			inflight--
			mu.Unlock()
			<-sem
		}(i, sh)
	}

	doneAll := make(chan struct{})
	go func() { wg.Wait(); close(doneAll) }()

	states := make([]ShardState, len(shards))
	received := 0
	for received < len(shards) {
		select {
		case o := <-results:
			states[o.index] = o.state
			received++
		case <-ctx.Done():
			// Deadline: collect only outcomes already buffered, then stop.
			// No new request can start (the dispatch loop above has exited
			// or sees ctx.Done at every slot acquire); in-flight requests
			// were canceled via ctx.
			for received < len(shards) {
				select {
				case o := <-results:
					states[o.index] = o.state
					received++
				default:
					r := finalize(states, shards, peak, started)
					if len(r.Missing) == len(shards) {
						return r, shard.ErrNoResults
					}
					return r, nil
				}
			}
		case <-doneAll:
			// Drain any buffered outcomes and finish.
			for received < len(shards) {
				o := <-results
				states[o.index] = o.state
				received++
			}
		}
	}
	r := finalize(states, shards, peak, started)
	if len(r.Missing) == len(shards) {
		return r, shard.ErrNoResults
	}
	return r, nil
}

// fetchOne invokes one shard and validates / de-duplicates its frames.
func fetchOne(ctx context.Context, sh shard.Shard) ShardState {
	st := ShardState{}
	dup := false
	first := true
	err := sh.Fetch(ctx, func(fr shard.Frame) {
		if !first {
			dup = true
			st.DupFrames = append(st.DupFrames, fr)
			return
		}
		first = false
		if len(fr.Records) != fr.Claimed {
			st.Status = shard.StatusCorrupt
			st.Err = shard.ErrCorrupt
			return
		}
		st.Status = shard.StatusOK
		st.Frame = fr
	})
	if st.Status == shard.StatusOK && dup {
		st.Status = shard.StatusDuplicate
		st.Err = shard.ErrDuplicate
		return st
	}
	if st.Status == shard.StatusOK {
		return st
	}
	if err != nil && st.Err == nil {
		st.Err = err
	}
	if st.Status == shard.StatusUnknown {
		if ctx.Err() != nil || errors.Is(err, shard.ErrTimeout) {
			st.Status = shard.StatusTimeout
		} else {
			st.Status = shard.StatusFailed
		}
	}
	return st
}

func finalize(states []ShardState, shards []shard.Shard, peak, started int) *Result {
	for i := range states {
		if states[i].Status == shard.StatusUnknown {
			states[i] = ShardState{
				ID: shards[i].ID(), Status: shard.StatusTimeout, Err: shard.ErrTimeout,
			}
		}
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].ID < states[j].ID })
	r := &Result{States: states, peakInflight: peak, started: started}
	for _, st := range states {
		if !st.Accepted() {
			r.Missing = append(r.Missing, st.ID)
		}
	}
	return r
}
