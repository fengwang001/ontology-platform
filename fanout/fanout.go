package fanout

import (
	"context"
	"errors"
	"sync"

	"ontology/shard"
)

var ErrNoShards = errors.New("no shards configured")

type Fanout struct {
	shards []shard.Shard
	limit  int
	mu     sync.Mutex
	inFly  int
	peak   int
}

func New(shards []shard.Shard, limit int) (*Fanout, error) {
	if len(shards) == 0 {
		return nil, ErrNoShards
	}
	if limit < 1 {
		limit = 1
	}
	return &Fanout{shards: append([]shard.Shard(nil), shards...), limit: limit}, nil
}

func (f *Fanout) Run(ctx context.Context) []shard.Attempt {
	if err := ctx.Err(); err != nil {
		attempts := make([]shard.Attempt, len(f.shards))
		for i, src := range f.shards {
			attempts[i] = shard.Attempt{ShardID: src.ID(), Status: shard.StatusTimeout, Err: err,
				Response: shard.Response{UpperBound: src.UpperBound()}}
		}
		return attempts
	}
	slots := make(chan struct{}, f.limit)
	results := make([]shard.Attempt, len(f.shards))
	var wg sync.WaitGroup
	for i, src := range f.shards {
		if err := ctx.Err(); err != nil {
			results[i] = shard.Attempt{ShardID: src.ID(), Status: shard.StatusTimeout, Err: err,
				Response: shard.Response{UpperBound: src.UpperBound()}}
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			results[i] = shard.Attempt{ShardID: src.ID(), Status: shard.StatusTimeout, Err: ctx.Err(),
				Response: shard.Response{UpperBound: src.UpperBound()}}
			continue
		}
		wg.Add(1)
		f.enter()
		go func(index int, source shard.Shard) {
			defer wg.Done()
			defer func() { <-slots }()
			defer f.leave()
			childCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			frames, err := source.Query(childCtx)
			attempt := shard.Attempt{
				ShardID: source.ID(), Status: shard.StatusOK, Launched: true, Frames: len(frames),
				Response: shard.Response{UpperBound: source.UpperBound()},
			}
			if err != nil {
				attempt.Status = shard.StatusTimeout
				attempt.Err = err
			} else if len(frames) == 0 {
				attempt.Status = shard.StatusFailed
				attempt.Err = errors.New("empty shard response batch")
			} else if frames[0].Declared != len(frames[0].Records) {
				attempt.Status = shard.StatusCorrupt
				attempt.Err = shard.ErrCorrupt
			} else {
				attempt.Response = frames[0]
			}
			results[index] = attempt
		}(i, src)
	}
	wg.Wait()
	return results
}

func (f *Fanout) enter() {
	f.mu.Lock()
	f.inFly++
	if f.inFly > f.peak {
		f.peak = f.inFly
	}
	f.mu.Unlock()
}

func (f *Fanout) leave() {
	f.mu.Lock()
	f.inFly--
	f.mu.Unlock()
}

func (f *Fanout) peakInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

func (f *Fanout) PeakInFlight() int { return f.peakInFlight() }
