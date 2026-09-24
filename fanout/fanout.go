// Package fanout 并发扇出查询，带并发上限、整体截止时间与故障分类。
package fanout

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"ontology/shard"
)

// Status 是单个分片的查询结局。
type Status int

const (
	StatusOK        Status = iota // 成功
	StatusTimeout                 // 整体截止到达，分片未返回
	StatusCorrupt                 // 自报条数与实际条数不符
	StatusError                   // 分片返回其他错误
	StatusCancelled               // 截止到达前未获得并发名额，未发起
)

// String 返回状态的可读名称。
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimeout:
		return "timeout"
	case StatusCorrupt:
		return "corrupt"
	case StatusError:
		return "error"
	default:
		return "cancelled"
	}
}

// Result 是单个分片的扇出结果，按输入顺序回填，与到达顺序无关。
type Result struct {
	ID     string
	Resp   shard.Response
	Status Status
	Err    error
}

// Fanout 控制并发扇出。peak 记录历史最大在飞请求数。
type Fanout struct {
	limit    int
	inflight atomic.Int64
	peak     atomic.Int64
	started  atomic.Int64
}

// New 构造并发上限为 limit 的扇出器；limit < 1 时按 1 处理。
func New(limit int) *Fanout {
	if limit < 1 {
		limit = 1
	}
	return &Fanout{limit: limit}
}

// Peak 返回历史最大同时在飞请求数。
func (f *Fanout) Peak() int64 { return f.peak.Load() }

// Started 返回实际发起的请求数（截止后不再增长）。
func (f *Fanout) Started() int64 { return f.started.Load() }

// Run 在 timeout 整体截止内并发查询所有分片，按输入顺序返回结果。
// 相同分片 ID 只查询首次出现的一个（去重，防止重试叠加重复计数）。
func (f *Fanout) Run(ctx context.Context, shards []shard.Shard, timeout time.Duration) []Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	uniq := make([]shard.Shard, 0, len(shards))
	seen := make(map[string]bool, len(shards))
	for _, s := range shards {
		if seen[s.ID()] {
			continue
		}
		seen[s.ID()] = true
		uniq = append(uniq, s)
	}
	results := make([]Result, len(uniq))
	sem := make(chan struct{}, f.limit)
	var wg sync.WaitGroup
	for i, s := range uniq {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = f.queryOne(ctx, sem, s)
		}()
	}
	wg.Wait()
	return results
}

func (f *Fanout) queryOne(ctx context.Context, sem chan struct{}, s shard.Shard) Result {
	res := Result{ID: s.ID()}
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		res.Status, res.Err = StatusCancelled, ctx.Err()
		return res
	}
	defer func() { <-sem }()
	n := f.inflight.Add(1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	f.started.Add(1)
	defer f.inflight.Add(-1)
	resp, err := s.Query(ctx)
	if err != nil {
		res.Err = err
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			res.Status = StatusTimeout
		case errors.Is(err, context.Canceled):
			res.Status = StatusCancelled
		default:
			res.Status = StatusError
		}
		return res
	}
	if resp.Claimed != len(resp.Records) {
		res.Status = StatusCorrupt
		return res
	}
	res.Resp, res.Status = resp, StatusOK
	return res
}
