package merger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func immediateQuery(v uint64) QueryFunc {
	return func(_ context.Context, _ ShardInfo) (Result, error) {
		return Result{Value: v, HasValue: true}, nil
	}
}

// 六类非法输入在发出任何请求前整体拒绝，且原因可区分。
func TestExecutorValidationRejectsBeforeSend(t *testing.T) {
	goodShards := []ShardInfo{{Name: "s1", RowBound: 10, ValueBound: 100}}
	called := false
	probe := QueryFunc(func(_ context.Context, _ ShardInfo) (Result, error) {
		called = true
		return Result{}, nil
	})
	cases := []struct {
		name        string
		shards      []ShardInfo
		agg         Aggregation
		concurrency int
		deadline    time.Duration
		want        error
	}{
		{"空分片列表", nil, Aggregation{Kind: AggCount}, 1, time.Second, ErrNoShards},
		{"分片名重复", []ShardInfo{goodShards[0], goodShards[0]}, Aggregation{Kind: AggCount}, 1, time.Second, ErrDuplicateShard},
		{"K 非正", goodShards, Aggregation{Kind: AggTopK, K: 0}, 1, time.Second, ErrInvalidK},
		{"并发上限非正", goodShards, Aggregation{Kind: AggCount}, 0, time.Second, ErrInvalidConcurrency},
		{"截止时长非正", goodShards, Aggregation{Kind: AggCount}, 1, 0, ErrInvalidDeadline},
		{"聚合未知", goodShards, Aggregation{Kind: AggKind(99)}, 1, time.Second, ErrUnknownAggregation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			ex := Executor{Concurrency: tc.concurrency, Deadline: tc.deadline, Query: probe}
			_, err := ex.Run(context.Background(), tc.shards, tc.agg)
			t.Logf("输入: shards=%+v agg=%+v concurrency=%d deadline=%s",
				tc.shards, tc.agg, tc.concurrency, tc.deadline)
			t.Logf("输出: err=%v called=%v", err, called)
			t.Logf("判定依据: errors.Is(err, %v) 成立且未发出任何请求", tc.want)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v, want errors.Is %v", err, tc.want)
			}
			if called {
				t.Fatal("校验失败前不应发出任何请求")
			}
		})
	}
}

// 在途请求数不超过上限，并报告峰值。
func TestExecutorPeakConcurrency(t *testing.T) {
	shards := []ShardInfo{
		{Name: "s1", RowBound: 10, ValueBound: 100},
		{Name: "s2", RowBound: 10, ValueBound: 100},
		{Name: "s3", RowBound: 10, ValueBound: 100},
		{Name: "s4", RowBound: 10, ValueBound: 100},
		{Name: "s5", RowBound: 10, ValueBound: 100},
		{Name: "s6", RowBound: 10, ValueBound: 100},
	}
	var inflight, observedPeak atomic.Int64
	query := QueryFunc(func(_ context.Context, _ ShardInfo) (Result, error) {
		cur := inflight.Add(1)
		for {
			p := observedPeak.Load()
			if cur <= p || observedPeak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inflight.Add(-1)
		return Result{Value: 1}, nil
	})
	ex := Executor{Concurrency: 2, Deadline: 5 * time.Second, Query: query}
	ans, err := ex.Run(context.Background(), shards, Aggregation{Kind: AggCount})
	t.Logf("输入: 6 个分片, 并发上限 2, 每请求耗时 20ms")
	t.Logf("输出: %+v (外部观测峰值=%d)", ans, observedPeak.Load())
	t.Logf("判定依据: 报告峰值 = 2, 外部观测峰值 <= 2, 全部成功时 count 精确值 = 6")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.PeakConcurrency != 2 {
		t.Fatalf("PeakConcurrency: got %d, want 2", ans.PeakConcurrency)
	}
	if observedPeak.Load() > 2 {
		t.Fatalf("在途请求数超过上限: observed peak=%d", observedPeak.Load())
	}
	if !ans.Complete || ans.Lower != 6 || ans.Upper != 6 {
		t.Fatalf("got %+v, want exact [6,6]", ans)
	}
}

// 截止时未响应的分片记为超时；截止后迟到的结果丢弃，不改变已给出的答案。
func TestExecutorDeadlineAndLateArrival(t *testing.T) {
	shards := []ShardInfo{
		{Name: "fast", RowBound: 10, ValueBound: 100},
		{Name: "slow", RowBound: 10, ValueBound: 100},
		{Name: "late", RowBound: 10, ValueBound: 100},
	}
	lateFinished := make(chan struct{})
	query := QueryFunc(func(ctx context.Context, s ShardInfo) (Result, error) {
		switch s.Name {
		case "fast":
			return Result{Value: 5}, nil
		case "slow":
			<-ctx.Done() // 一直阻塞到截止
			return Result{}, ctx.Err()
		case "late":
			time.Sleep(500 * time.Millisecond) // 远超截止时长, 迟到返回
			close(lateFinished)
			return Result{Value: 777}, nil
		}
		return Result{}, nil
	})
	ex := Executor{Concurrency: 3, Deadline: 100 * time.Millisecond, Query: query}
	ans, err := ex.Run(context.Background(), shards, Aggregation{Kind: AggCount})
	t.Logf("输入: fast 立即返回 5; slow 阻塞至截止; late 在截止后迟到返回 777")
	t.Logf("输出: %+v", ans)
	t.Logf("判定依据: Timeouts=2(slow,late); 下界 = 5; 上界 = 5 + (10+10) = 25; " +
		"迟到的 777 被丢弃, 不计入成功")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.Timeouts != 2 || ans.Succeeded != 1 {
		t.Fatalf("got Timeouts=%d Succeeded=%d, want 2/1", ans.Timeouts, ans.Succeeded)
	}
	if ans.Lower != 5 || ans.Upper != 25 {
		t.Fatalf("got [%d,%d], want [5,25]", ans.Lower, ans.Upper)
	}

	// 等待迟到的结果实际返回后，已给出的答案不受影响。
	<-lateFinished
	t.Logf("迟到结果已返回后复查: %+v", ans)
	t.Logf("判定依据: 答案为值语义快照, 迟到结果不改变任何字段")
	if ans.Lower != 5 || ans.Upper != 25 || ans.Succeeded != 1 {
		t.Fatalf("答案被迟到结果改变: %+v", ans)
	}
}

// 分片返回错误按缺失处理并计数。
func TestExecutorShardError(t *testing.T) {
	shards := []ShardInfo{
		{Name: "good", RowBound: 10, ValueBound: 100},
		{Name: "bad", RowBound: 10, ValueBound: 100},
	}
	query := QueryFunc(func(_ context.Context, s ShardInfo) (Result, error) {
		if s.Name == "bad" {
			return Result{}, errors.New("boom")
		}
		return Result{Value: 4}, nil
	})
	ex := Executor{Concurrency: 2, Deadline: time.Second, Query: query}
	ans, err := ex.Run(context.Background(), shards, Aggregation{Kind: AggCount})
	t.Logf("输入: good 返回 4; bad 返回错误 boom")
	t.Logf("输出: %+v", ans)
	t.Logf("判定依据: Errors=1; 下界 = 4; 上界 = 4 + 缺失行数上界 10 = 14")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.Errors != 1 || ans.Lower != 4 || ans.Upper != 14 {
		t.Fatalf("got %+v, want Errors=1 [4,14]", ans)
	}
}
