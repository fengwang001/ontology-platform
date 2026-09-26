// Package lim 在令牌桶本体之上持有单调时钟时间戳 last，
// 负责参数校验、补令牌调度与消费，依赖 tkn，不依赖 api。
package lim

import (
	"errors"
	"sync"

	"ontology/tkn"
)

// 可判定的哨兵错误：需求非法、时钟回退。二者互不相同，
// 且与 api.ErrInvalidConfig 不同。
var (
	ErrInvalidNeed   = errors.New("lim: need must be >= 1")
	ErrClockRollback = errors.New("lim: timestamp went backwards")
)

// Limiter 持有一个 tkn.Bucket 与最近一次时间戳。
type Limiter struct {
	mu     sync.Mutex
	bucket *tkn.Bucket
	last   int64
}

// New 创建限流器。capacity、rate 的合法性由上层 api 校验。
func New(capacity, rate int64) *Limiter {
	return &Limiter{
		bucket: tkn.New(capacity, rate),
	}
}

// Allow 判定时间戳 t 到达的、需要 need 个令牌的请求是否放行。
// need < 1 或 t < last 时返回哨兵错误且不改变任何状态；
// 令牌不足是正常结果（false, nil），且 last 仍会随补令牌推进。
func (l *Limiter) Allow(t, need int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 前置条件全部在任何状态写入之前校验：失败不留痕。
	if need < 1 {
		return false, ErrInvalidNeed
	}
	if t < l.last {
		return false, ErrClockRollback
	}

	elapsed := t - l.last
	l.bucket.Refill(elapsed)
	l.last = t // 无条件推进：被拒不补时钟会破坏与朴素参照的一致性。

	return l.bucket.TryConsume(need), nil
}

// Tokens 返回当前令牌数，可与 Allow 并发调用。
func (l *Limiter) Tokens() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bucket.Tokens()
}
