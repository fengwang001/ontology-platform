package merger

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// QueryFunc 向单个分片发起聚合查询。
type QueryFunc func(ctx context.Context, shard ShardInfo) (Result, error)

// Executor 并发扇出执行器：在并发上限与截止时间内收集各分片结果。
type Executor struct {
	Concurrency int           // 同时在途请求数上限
	Deadline    time.Duration // 从 Run 开始的截止时长
	Query       QueryFunc
}

// Run 校验参数后扇出查询并合并结果。
// 截止时仍未响应的分片记为超时；截止后迟到的结果直接丢弃，
// 不改变已经给出的答案。
func (e Executor) Run(ctx context.Context, shards []ShardInfo, agg Aggregation) (Answer, error) {
	if err := Validate(shards, agg, e.Concurrency, e.Deadline); err != nil {
		return Answer{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, e.Deadline)
	defer cancel()

	type outcome struct {
		shard string
		res   Result
		err   error
	}
	// 缓冲容量等于分片数，保证迟到 goroutine 的发送永不阻塞。
	ch := make(chan outcome, len(shards))
	sem := make(chan struct{}, e.Concurrency)
	var inflight, peak atomic.Int64
	var wg sync.WaitGroup

	for _, s := range shards {
		wg.Add(1)
		go func(s ShardInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cur := inflight.Add(1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			res, err := e.Query(ctx, s)
			inflight.Add(-1)
			ch <- outcome{s.Name, res, err}
		}(s)
	}
	go func() { wg.Wait(); close(ch) }()

	responded := make(map[string]struct{}, len(shards))
	responses := make([]ShardResponse, 0, len(shards))
loop:
	for {
		select {
		case o, ok := <-ch:
			if !ok {
				break loop
			}
			if ctx.Err() != nil {
				continue // 截止后迟到，丢弃
			}
			responded[o.shard] = struct{}{}
			if o.err != nil {
				kind := RespError
				if errors.Is(o.err, context.DeadlineExceeded) {
					kind = RespTimeout
				}
				responses = append(responses, ShardResponse{Shard: o.shard, Kind: kind})
				continue
			}
			responses = append(responses, ShardResponse{Shard: o.shard, Kind: RespOK, Result: o.res})
		case <-ctx.Done():
			// 截止：未响应的分片记为超时，之后到达的结果一律丢弃。
			for _, s := range shards {
				if _, ok := responded[s.Name]; !ok {
					responses = append(responses, ShardResponse{Shard: s.Name, Kind: RespTimeout})
				}
			}
			break loop
		}
	}

	ans := Merge(shards, agg, responses)
	ans.PeakConcurrency = int(peak.Load())
	return ans, nil
}
