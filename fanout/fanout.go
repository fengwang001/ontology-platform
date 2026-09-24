// Package fanout fans an aggregate query out to shards with bounded
// concurrency, an overall deadline, retry de-duplication and per-shard
// status classification.
package fanout

import (
	"context"
	"errors"
	"sync"
	"time"

	"ontology/shard"
)

// Status classifies the outcome for one shard.
type Status int

const (
	StatusUnknown Status = iota
	StatusOK
	StatusTimeout
	StatusCorrupt
	StatusDuplicate
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimeout:
		return "timeout"
	case StatusCorrupt:
		return "corrupt"
	case StatusDuplicate:
		return "duplicate"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ErrNoShards is returned when the shard list is empty.
var ErrNoShards = errors.New("fanout: no shards")

// ShardResult is one shard's classified outcome.
type ShardResult struct {
	ID       string
	Status   Status
	Response shard.Response
}

// Outcome is the full fan-out result.
type Outcome struct {
	Results []ShardResult
}

// OK returns the successfully delivered responses.
func (o Outcome) OK() []ShardResult {
	var ok []ShardResult
	for _, r := range o.Results {
		if r.Status == StatusOK || r.Status == StatusDuplicate {
			ok = append(ok, r)
		}
	}
	return ok
}

// Missing returns non-OK statuses.
func (o Outcome) Missing() []ShardResult {
	var miss []ShardResult
	for _, r := range o.Results {
		if r.Status != StatusOK && r.Status != StatusDuplicate {
			miss = append(miss, r)
		}
	}
	return miss
}

// Fan coordinates the fan-out.
type Fan struct {
	shards    []shard.Shard
	maxFlight int

	mu            sync.Mutex
	inFlight      int
	peakInFlight  int
	startedAfterD bool
}

// New builds a Fan. maxFlight must be positive.
func New(shards []shard.Shard, maxFlight int) (*Fan, error) {
	if len(shards) == 0 {
		return nil, ErrNoShards
	}
	if maxFlight < 1 {
		maxFlight = 1
	}
	return &Fan{shards: shards, maxFlight: maxFlight}, nil
}

// PeakInFlight reports the historical maximum simultaneous in-flight fetches.
func (f *Fan) PeakInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peakInFlight
}

// StartedAfterDeadline reports whether any fetch started after the deadline.
func (f *Fan) StartedAfterDeadline() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startedAfterD
}

func (f *Fan) enter() {
	f.mu.Lock()
	f.inFlight++
	if f.inFlight > f.peakInFlight {
		f.peakInFlight = f.inFlight
	}
	f.mu.Unlock()
}

func (f *Fan) leave() {
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
}

// Run fans out and waits for every attempt to settle within the deadline.
func (f *Fan) Run(ctx context.Context, deadline time.Duration) Outcome {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	results := make([]ShardResult, len(f.shards))
	sem := make(chan struct{}, f.maxFlight)
	var wg sync.WaitGroup

	for i, sh := range f.shards {
		if ctx.Err() != nil {
			results[i] = ShardResult{ID: sh.ID(), Status: StatusTimeout}
			f.mu.Lock()
			f.startedAfterD = true
			f.mu.Unlock()
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = ShardResult{ID: sh.ID(), Status: StatusTimeout}
			f.mu.Lock()
			f.startedAfterD = true
			f.mu.Unlock()
			continue
		}

		wg.Add(1)
		f.enter()
		go func(idx int, s shard.Shard) {
			defer wg.Done()
			defer func() { <-sem }()
			defer f.leave()
			results[idx] = f.fetchOne(ctx, s)
		}(i, sh)
	}
	wg.Wait()
	return Outcome{Results: results}
}

func (f *Fan) fetchOne(ctx context.Context, s shard.Shard) ShardResult {
	resp, err := s.Fetch(ctx, 1)
	status := StatusFailed
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status = StatusTimeout
	case err == nil:
		if shard.Validate(resp) != nil {
			status = StatusCorrupt
		} else {
			status = StatusOK
		}
	}
	if status == StatusFailed && errors.Is(err, shard.ErrRetryable) {
		resp, err = s.Fetch(ctx, 1)
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			status = StatusTimeout
		case err == nil && shard.Validate(resp) == nil:
			status = StatusDuplicate
		default:
			status = StatusFailed
		}
	}
	return ShardResult{ID: s.ID(), Status: status, Response: resp}
}
