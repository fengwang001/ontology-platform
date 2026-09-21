package retry

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Runner 按固定策略调度重试。零值之外的字段通过 New 注入，
// Runner 可重复使用，且可安全地被多个 goroutine 并发调用 Do。
type Runner struct {
	policy Policy
	sleep  func(time.Duration)
	rnd    func() float64

	mu     sync.Mutex
	delays []time.Duration // 最近一次 Do 的等待序列
}

// New 构造一个 Runner。sleep 为 nil 时使用 time.Sleep，
// rnd 为 nil 时使用默认随机源（返回 [0,1)）。
func New(p Policy, sleep func(time.Duration), rnd func() float64) *Runner {
	if sleep == nil {
		sleep = time.Sleep
	}
	if rnd == nil {
		rnd = rand.Float64
	}
	return &Runner{policy: p, sleep: sleep, rnd: rnd}
}

// Do 执行 fn，失败时按策略退避重试，返回成功时的尝试序号
// （从 1 起）与最后一次的错误。每次 Do 使用独立的运行状态，
// 互不干扰。
func (r *Runner) Do(fn func(attempt int) error) (int, error) {
	max := r.policy.maxAttempts()
	delays := make([]time.Duration, 0, max-1)
	var lastErr error
	for attempt := 1; ; attempt++ {
		err := fn(attempt)
		if err == nil {
			r.record(delays)
			return attempt, nil
		}
		if isPermanent(err) {
			r.record(delays)
			return attempt, fmt.Errorf("%w", ErrAborted)
		}
		lastErr = err
		if attempt >= max {
			break // 最后一次失败后绝不再等
		}
		d := r.policy.delay(attempt, r.rnd)
		delays = append(delays, d)
		r.sleep(d)
	}
	r.record(delays)
	return max, fmt.Errorf("%w: %w", ErrExhausted, lastErr)
}

// Delays 返回最近一次 Do 实际等待过的间隔，按发生顺序。
func (r *Runner) Delays() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Duration, len(r.delays))
	copy(out, r.delays)
	return out
}

// record 用本次运行的等待序列替换上一次的记录。
func (r *Runner) record(delays []time.Duration) {
	r.mu.Lock()
	r.delays = delays
	r.mu.Unlock()
}
