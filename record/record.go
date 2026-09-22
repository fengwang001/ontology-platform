package record

import (
	"time"

	"ontology/digest"
)

// Record 是单个幂等键的状态机。
// 由 gateway 持有的互斥锁统一保护，自身不再加锁；
// 并发重复提交通过 done 通道等待首次执行完成。
type Record struct {
	fp       digest.Fingerprint
	state    State
	deadline time.Time
	result   []byte
	err      error
	attempts int
	done     chan struct{}
}

// New 创建一条执行中的记录，ttl 从 now 起算（左闭右开）。
func New(fp digest.Fingerprint, now time.Time, ttl time.Duration) *Record {
	return &Record{
		fp:       fp,
		state:    Running,
		deadline: now.Add(ttl),
		attempts: 1,
		done:     make(chan struct{}),
	}
}

// Fingerprint 返回首次（或当前重试）绑定的请求体指纹。
func (r *Record) Fingerprint() digest.Fingerprint { return r.fp }

// State 返回当前状态。
func (r *Record) State() State { return r.state }

// Attempts 返回该记录已真正发起执行的次数。
func (r *Record) Attempts() int { return r.attempts }

// Done 在记录离开执行中状态（成功或失败）时关闭。
func (r *Record) Done() <-chan struct{} { return r.done }

// Result 返回成功结果的副本；未成功时返回 nil。
func (r *Record) Result() []byte {
	if r.state != Succeeded || r.result == nil {
		return nil
	}
	out := make([]byte, len(r.result))
	copy(out, r.result)
	return out
}

// Err 返回失败时记录的原始错误；其余状态为 nil。
func (r *Record) Err() error { return r.err }

// Succeed 把记录固定为成功并保存结果，随后放行所有等待者。
func (r *Record) Succeed(result []byte) {
	r.state = Succeeded
	r.err = nil
	if result != nil {
		r.result = append([]byte(nil), result...)
	}
	close(r.done)
}

// Fail 把记录记为失败并原样保存错误，随后放行所有等待者。
func (r *Record) Fail(err error) {
	r.state = Failed
	r.err = err
	r.result = nil
	close(r.done)
}

// Retry 将一条失败记录重置为新一次执行，并重置存活计时。
func (r *Record) Retry(fp digest.Fingerprint, now time.Time, ttl time.Duration) {
	r.fp = fp
	r.state = Running
	r.err = nil
	r.result = nil
	r.deadline = now.Add(ttl)
	r.attempts++
	r.done = make(chan struct{})
}

// ExpiredAt 按给定时刻做左闭右开的过期判定：now 恰好等于过期时刻即过期。
// 执行中的记录永不过期，以保证长耗时执行不被清理影响。
func (r *Record) ExpiredAt(now time.Time) bool {
	if r.state == Running {
		return false
	}
	return !now.Before(r.deadline)
}

// TTL 返回从 now 起算的剩余存活时长；已过期返回 0。
// 执行中的记录也返回正剩余（过期不影响执行，但仍展示名义剩余）。
func (r *Record) TTL(now time.Time) time.Duration {
	remaining := r.deadline.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}
