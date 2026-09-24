// Package fanout 并发扇出查询，带并发上限、整体截止时间与去重。
package fanout

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"ontology/shard"
)

var (
	ErrNoShards        = errors.New("fanout: no shards")
	ErrAllShardsFailed = errors.New("fanout: all shards failed")
)

type Outcome struct {
	Success []shard.Response
	Missing []string
	Status  map[string]shard.Status
	Peak    int
	Started int
}

// Run 并发查询。maxConc<=0 视为 1；timeout<=0 表示无整体截止。
func Run(parent context.Context, shards []shard.Shard, maxConc int, timeout time.Duration) (Outcome, error) {
	if len(shards) == 0 {
		return Outcome{Status: map[string]shard.Status{}}, ErrNoShards
	}
	if maxConc <= 0 {
		maxConc = 1
	}
	ctx, cancel := context.WithCancel(parent)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	}
	defer cancel()

	r := &runner{
		ctx:    ctx,
		sem:    make(chan struct{}, maxConc),
		done:   make(chan shard.Response, len(shards)*2),
		status: map[string]shard.Status{},
		oks:    map[string]shard.Response{},
		seen:   map[string]bool{},
		total:  len(shards),
	}
	for _, s := range shards {
		r.status[s.ID()] = shard.StatusUnknown
	}
	go r.dispatch(shards)
	r.collect()

	out := Outcome{Status: r.status, Peak: r.peak, Started: r.started}
	for id, st := range r.status {
		if st == shard.StatusOK {
			out.Success = append(out.Success, r.oks[id])
		} else {
			out.Missing = append(out.Missing, id)
		}
	}
	sort.Slice(out.Success, func(i, j int) bool { return out.Success[i].ID < out.Success[j].ID })
	sort.Strings(out.Missing)
	if len(out.Success) == 0 {
		return out, ErrAllShardsFailed
	}
	return out, nil
}

type runner struct {
	ctx      context.Context
	sem      chan struct{}
	done     chan shard.Response
	status   map[string]shard.Status
	oks      map[string]shard.Response
	seen     map[string]bool
	total    int
	inflight int
	peak     int
	started  int
	mu       sync.Mutex
	wg       sync.WaitGroup
}

func (r *runner) dispatch(shards []shard.Shard) {
	for _, s := range shards {
		select {
		case r.sem <- struct{}{}:
		case <-r.ctx.Done():
			r.wg.Wait()
			return
		}
		r.wg.Add(1)
		r.mu.Lock()
		r.started++
		r.inflight++
		if r.inflight > r.peak {
			r.peak = r.inflight
		}
		r.mu.Unlock()
		go func(s shard.Shard) {
			defer r.wg.Done()
			defer func() {
				r.mu.Lock()
				r.inflight--
				r.mu.Unlock()
				<-r.sem
			}()
			resp := s.Query(r.ctx)
			r.done <- resp
			for _, ex := range resp.Extras() {
				r.done <- ex
			}
		}(s)
	}
}

// collect 收集每个分片的首个响应；截止后等待在飞请求取消，其余标记 canceled。
func (r *runner) collect() {
	finished := map[string]bool{}
	allDone := make(chan struct{})
	go func() { r.wg.Wait(); close(allDone) }()
	for {
		select {
		case resp := <-r.done:
			r.record(resp)
			finished[resp.ID] = true
			if len(finished) == r.total {
				return
			}
		case <-allDone:
			r.drain(finished)
			if len(finished) == r.total {
				return
			}
			// 仍有未发起或未响应：只可能在截止后发生。
			if r.ctx.Err() == nil {
				<-r.ctx.Done()
			}
			<-allDone
			r.drain(finished)
			r.markCanceled(finished)
			return
		case <-r.ctx.Done():
			<-allDone
			r.drain(finished)
			r.markCanceled(finished)
			return
		}
	}
}

func (r *runner) drain(finished map[string]bool) {
	for {
		select {
		case resp := <-r.done:
			r.record(resp)
			finished[resp.ID] = true
		default:
			return
		}
	}
}

func (r *runner) record(resp shard.Response) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[resp.ID] {
		return
	}
	r.seen[resp.ID] = true
	st := classify(resp)
	r.status[resp.ID] = st
	if st == shard.StatusOK {
		r.oks[resp.ID] = resp
	}
}

func (r *runner) markCanceled(finished map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.status {
		if !finished[id] {
			r.status[id] = shard.StatusCanceled
			finished[id] = true
		}
	}
}

func classify(resp shard.Response) shard.Status {
	switch {
	case errors.Is(resp.Err, shard.ErrCorrupt):
		return shard.StatusCorrupt
	case errors.Is(resp.Err, shard.ErrTimeout):
		return shard.StatusTimedOut
	case resp.Err != nil:
		return shard.StatusCanceled
	case resp.Claimed != len(resp.Records):
		return shard.StatusCorrupt
	default:
		return shard.StatusOK
	}
}
