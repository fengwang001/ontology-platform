// Package tok 是整数令牌桶 + 逻辑时钟：不依赖其他包。
// 补充有基准速率 rate 与追赶速率 cap 两种，补后令牌封顶 burst。
package tok

import "errors"

// ErrClockRollback：Advance 的目标时刻早于当前时钟。
var ErrClockRollback = errors.New("tok: clock moved backwards")

// ErrInvalidParam：rate<=0、catch<rate 或 burst<=0。
var ErrInvalidParam = errors.New("tok: invalid parameter")

// Bucket 是非导出状态的令牌桶。时间与令牌均为非负整数。
type Bucket struct {
	now    int64
	tokens int64
	rate   int64
	catch  int64
	burst  int64
}

// New 构造满桶：now=0、tokens=burst。rate>0、catch>=rate、burst>0。
func New(rate, catch, burst int64) (*Bucket, error) {
	if rate <= 0 || catch < rate || burst <= 0 {
		return nil, ErrInvalidParam
	}
	return &Bucket{now: 0, tokens: burst, rate: rate, catch: catch, burst: burst}, nil
}

// Advance 把逻辑时钟推进到绝对时刻 t。
// nonempty 表示本次调用开始时刻队列是否非空：非空按 catch 补，否则按 rate 补。
// t<now 报 ErrClockRollback 且状态不变；t==now 幂等。
func (b *Bucket) Advance(t int64, nonempty bool) error {
	if t < b.now {
		return ErrClockRollback
	}
	if t == b.now {
		return nil
	}
	r := b.rate
	if nonempty {
		r = b.catch
	}
	dt := t - b.now
	want := b.burst - b.tokens
	// 只补到 burst 为止，顺带避免 r*dt 溢出。
	if dt >= (want+r-1)/r {
		b.tokens = b.burst
	} else {
		b.tokens += r * dt
	}
	b.now = t
	return nil
}

// Take 至多取走 n 个令牌，返回实际取走数（受当前令牌数限制）。
func (b *Bucket) Take(n int) int {
	if n <= 0 {
		return 0
	}
	have := int(b.tokens)
	if n > have {
		n = have
	}
	b.tokens -= int64(n)
	return n
}

// Now 返回当前逻辑时刻。
func (b *Bucket) Now() int64 { return b.now }

// Tokens 返回当前令牌数。
func (b *Bucket) Tokens() int64 { return b.tokens }
