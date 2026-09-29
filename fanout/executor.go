package fanout

import (
	"context"
	"sync/atomic"
)

// kind tags what happened with one shard.
type outcomeKind int

const (
	outcomeError outcomeKind = iota + 1
	outcomeResult
	outcomeDuplicate
)

type shardOutcome struct {
	shardIndex int
	kind       outcomeKind
	result     ShardResult
}

// Execute validates the request, fans out to every shard bounded by the
// concurrency limit and deadline, and merges the results that arrive in time.
// Validation failures reject the whole request before any call is issued.
func Execute(ctx context.Context, req Request) (Answer, Stats, error) {
	if err := validate(req); err != nil {
		return Answer{}, Stats{}, err
	}

	total := len(req.Shards)
	stats := Stats{}

	queryCtx, cancel := context.WithTimeout(ctx, req.Deadline)
	defer cancel()

	outcomes := make(chan shardOutcome, total)
	sem := make(chan struct{}, req.Concurrency)

	// Collector state. All mutation happens in this goroutine, so merging is
	// independent of arrival order.
	merger := newMerger(req)
	finished := make(map[int]bool, total)
	succeeded := make(map[int]bool, total)
	var inFlight atomic.Int64
	var peak atomic.Int64

	launch := func(i int) {
		sem <- struct{}{}
		cur := inFlight.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		go func(index int) {
			defer func() {
				<-sem
				inFlight.Add(-1)
			}()
			spec := req.Shards[index]
			emitted := false
			emit := func(res ShardResult) {
				if emitted {
					select {
					case outcomes <- shardOutcome{shardIndex: index, kind: outcomeDuplicate}:
					case <-queryCtx.Done():
					}
					return
				}
				emitted = true
				select {
				case outcomes <- shardOutcome{shardIndex: index, kind: outcomeResult, result: res}:
				case <-queryCtx.Done():
				}
			}
			err := req.Client.Query(queryCtx, spec, req.Agg, req.K, emit)
			if err != nil {
				select {
				case outcomes <- shardOutcome{shardIndex: index, kind: outcomeError}:
				case <-queryCtx.Done():
				}
			}
		}(i)
	}

	for i := range req.Shards {
		launch(i)
	}

	for len(finished) < total {
		select {
		case <-queryCtx.Done():
			// Deadline (or parent cancellation) reached. Anything still
			// outstanding is missing; late arrivals are drained and ignored.
			for i := range req.Shards {
				if !finished[i] {
					finished[i] = true
					stats.TimedOut++
				}
			}
		case oc := <-outcomes:
			switch oc.kind {
			case outcomeDuplicate:
				// Only the first result counts; later arrivals are discarded
				// even if they land after the shard otherwise settled.
				stats.Duplicate++
				continue
			}
			if finished[oc.shardIndex] {
				// Late non-duplicate arrival (typically after the deadline):
				// discard without changing the answer or counters.
				continue
			}
			switch oc.kind {
			case outcomeError:
				finished[oc.shardIndex] = true
				stats.Failed++
			case outcomeResult:
				if err := checkBounds(req.Shards[oc.shardIndex], req.Agg, oc.result); err != nil {
					finished[oc.shardIndex] = true
					stats.BoundViolation++
					continue
				}
				finished[oc.shardIndex] = true
				stats.Succeeded++
				succeeded[oc.shardIndex] = true
				merger.add(oc.result)
			}
		}
	}

	stats.PeakConcurrent = int(peak.Load())
	missing := make([]ShardSpec, 0, total-stats.Succeeded)
	for i, spec := range req.Shards {
		if !succeeded[i] {
			missing = append(missing, spec)
		}
	}
	answer := merger.answer(missing)
	return answer, stats, nil
}
