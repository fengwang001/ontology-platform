// Package fanout fans a query out to shards with a concurrency cap and a
// global deadline, collecting per-shard status and deduplicating records.
package fanout

import (
	"context"
	"errors"
	"sync"
	"time"

	"ontology/shard"
)

// Status is the outcome of one shard query.
type Status int

const (
	StatusOK      Status = iota // full data collected
	StatusTimeout               // did not finish before the deadline
	StatusError                 // the shard returned an error
	StatusCorrupt               // delivered count != claimed count
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimeout:
		return "timeout"
	case StatusError:
		return "error"
	case StatusCorrupt:
		return "corrupt"
	}
	return "unknown"
}

// Outcome is the result for one shard.
type Outcome struct {
	ShardID string
	Status  Status
	Records []shard.Record
	Bound   float64
}

// Result is the fan-out result.
type Result struct {
	Outcomes []Outcome
	Order    []string
	peak     int
}

// PeakInFlight reports the historical maximum simultaneous requests.
func (r *Result) PeakInFlight() int { return r.peak }

var (
	// ErrNoShards means the query targeted zero shards.
	ErrNoShards = errors.New("fanout: no shards")
	// ErrAllFailed means every shard failed and nothing can be combined.
	ErrAllFailed = errors.New("fanout: all shards failed")
)

type limiter struct {
	mu       sync.Mutex
	inFlight int
	peak     int
}

func (l *limiter) acquire() {
	l.mu.Lock()
	l.inFlight++
	if l.inFlight > l.peak {
		l.peak = l.inFlight
	}
	l.mu.Unlock()
}

func (l *limiter) release() {
	l.mu.Lock()
	l.inFlight--
	l.mu.Unlock()
}

// Run queries all shards. At most maxConcurrent requests are in flight and no
// new request starts after the deadline.
func Run(ctx context.Context, shards []shard.Shard, maxConcurrent int, deadline time.Duration) (*Result, error) {
	if len(shards) == 0 {
		return nil, ErrNoShards
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	lim := &limiter{}
	sem := make(chan struct{}, maxConcurrent)
	resCh := make(chan Outcome, len(shards))
	var wg sync.WaitGroup

	for _, s := range shards {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return finish(shards, resCh, lim.peak)
		}
		wg.Add(1)
		go func(s shard.Shard) {
			defer wg.Done()
			defer func() { <-sem }()
			lim.acquire()
			defer lim.release()
			resCh <- queryOne(ctx, s)
		}(s)
	}
	wg.Wait()
	return finish(shards, resCh, lim.peak)
}

func queryOne(ctx context.Context, s shard.Shard) Outcome {
	o := Outcome{ShardID: s.ID(), Bound: s.Bound()}
	ch := make(chan []shard.Record, 8)
	done := make(chan error, 1)
	go func() { done <- s.Query(ctx, ch) }()

	seen := map[key]struct{}{}
	var recs []shard.Record
	for {
		select {
		case <-ctx.Done():
			o.Status = StatusTimeout
			return o
		case chunk, ok := <-ch:
			if !ok {
				o.Records = recs
				o.Status = classify(<-done, len(recs), s.Claimed())
				return o
			}
			for _, rec := range chunk {
				k := keyOf(rec)
				if _, dup := seen[k]; dup {
					continue
				}
				seen[k] = struct{}{}
				recs = append(recs, rec)
			}
		}
	}
}

func classify(err error, got, claimed int) Status {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return StatusTimeout
	case err != nil:
		return StatusError
	case got != claimed:
		return StatusCorrupt
	default:
		return StatusOK
	}
}

func finish(shards []shard.Shard, ch chan Outcome, peak int) (*Result, error) {
	byID := map[string]Outcome{}
	ok := 0
	close(ch)
	for o := range ch {
		byID[o.ShardID] = o
		if o.Status == StatusOK {
			ok++
		}
	}
	outs := make([]Outcome, 0, len(shards))
	order := make([]string, len(shards))
	for i, s := range shards {
		order[i] = s.ID()
		if o, have := byID[s.ID()]; have {
			outs = append(outs, o)
		} else {
			outs = append(outs, Outcome{ShardID: s.ID(), Status: StatusTimeout, Bound: s.Bound()})
		}
	}
	res := &Result{Outcomes: outs, Order: order, peak: peak}
	if ok == 0 {
		return res, ErrAllFailed
	}
	return res, nil
}

type key struct {
	id string
	v  float64
}

func keyOf(r shard.Record) key { return key{id: r.ID, v: r.V} }
