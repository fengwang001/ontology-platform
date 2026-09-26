// Package api 是限流器的对外入口，依赖 lim，不被 tkn/lim 依赖。
package api

import (
	"errors"
	"fmt"

	"ontology/lim"
)

// 三类互不相同的可判定哨兵错误。
var (
	ErrInvalidConfig = errors.New("api: capacity and rate must be >= 1")
	ErrInvalidNeed   = lim.ErrInvalidNeed
	ErrClockRollback = lim.ErrClockRollback
)

// Limiter 是对外限流器。
type Limiter struct {
	inner *lim.Limiter
}

// New 以容量 capacity、每秒补入速率 rate 创建限流器；
// capacity 或 rate < 1 返回 ErrInvalidConfig，且不返回限流器。
func New(capacity, rate int64) (*Limiter, error) {
	if capacity < 1 || rate < 1 {
		return nil, ErrInvalidConfig
	}
	return &Limiter{inner: lim.New(capacity, rate)}, nil
}

// Allow 判定时间戳 t 到达的、需要 need 个令牌的请求是否放行。
func (l *Limiter) Allow(t, need int64) (bool, error) {
	return l.inner.Allow(t, need)
}

// Tokens 返回当前令牌数。
func (l *Limiter) Tokens() int64 {
	return l.inner.Tokens()
}

type req struct {
	t, need int64
}

// builtIn 是 SelfCheck 使用的内置请求序列（含第三节八步与额外多档节拍）。
var builtIn = []req{
	{0, 15}, {0, 8}, {3, 10}, {3, 5}, {8, 12}, {9, 6}, {20, 18}, {20, 3},
	{20, 1}, {21, 1}, {100, 50}, {100, 1}, {101, 3}, {101, 1},
}

// naiveRef 用朴素参照逐步推进，返回每步的判定与判定后令牌数。
func naiveRef(capacity, rate int64, rs []req) []struct {
	allow  bool
	tokens int64
} {
	out := make([]struct {
		allow  bool
		tokens int64
	}, len(rs))
	ref, prevT := capacity, int64(0)
	for i, r := range rs {
		ref = min(capacity, ref+(r.t-prevT)*rate)
		prevT = r.t
		allow := ref >= r.need
		if allow {
			ref -= r.need
		}
		out[i].allow, out[i].tokens = allow, ref
	}
	return out
}

// SelfCheck 用内置请求序列核验四条不变量，全部通过返回 nil。
// 它只在内部新建临时限流器，不改动接收者状态，可被测试直接调用。
func (l *Limiter) SelfCheck() error {
	const capacity, rate = int64(20), int64(2)

	// 不变量 1（与朴素参照一致）与 2（令牌不越界）。
	want := naiveRef(capacity, rate, builtIn)
	got, err := New(capacity, rate)
	if err != nil {
		return err
	}
	for i, r := range builtIn {
		allow, e := got.Allow(r.t, r.need)
		if e != nil || allow != want[i].allow || got.Tokens() != want[i].tokens {
			return fmt.Errorf("naive/bounds mismatch at step %d: got (%v,%d) want (%v,%d)",
				i, allow, got.Tokens(), want[i].allow, want[i].tokens)
		}
		if toks := got.Tokens(); toks < 0 || toks > capacity {
			return fmt.Errorf("tokens out of bounds at step %d: %d", i, toks)
		}
	}

	// 不变量 3（确定性）：同序列喂入两个新限流器，逐拍必须一致。
	d1, _ := New(capacity, rate)
	d2, _ := New(capacity, rate)
	for i, r := range builtIn {
		a1, e1 := d1.Allow(r.t, r.need)
		a2, e2 := d2.Allow(r.t, r.need)
		if a1 != a2 || !errors.Is(e1, e2) || d1.Tokens() != d2.Tokens() {
			return fmt.Errorf("non-deterministic at step %d", i)
		}
	}

	// 不变量 4（失败不留痕）：非法请求后状态不变，且后续行为与未受扰动的对照桶一致。
	fail, _ := New(capacity, rate)
	ctrl, _ := New(capacity, rate)
	before := fail.Tokens()
	if _, e := fail.Allow(5, 0); !errors.Is(e, ErrInvalidNeed) {
		return fmt.Errorf("want ErrInvalidNeed, got %v", e)
	}
	if _, e := fail.Allow(-1, 1); !errors.Is(e, ErrClockRollback) {
		return fmt.Errorf("want ErrClockRollback, got %v", e)
	}
	if fail.Tokens() != before {
		return fmt.Errorf("failure changed tokens: %d -> %d", before, fail.Tokens())
	}
	for _, r := range []req{{5, 4}, {6, 4}, {7, 4}} {
		a, e := fail.Allow(r.t, r.need)
		c, _ := ctrl.Allow(r.t, r.need)
		if a != c || e != nil || fail.Tokens() != ctrl.Tokens() {
			return fmt.Errorf("limiter diverged from control after failed calls at (%d,%d)", r.t, r.need)
		}
	}

	// 三类哨兵错误必须互不相同。
	if errors.Is(ErrInvalidConfig, ErrInvalidNeed) ||
		errors.Is(ErrInvalidNeed, ErrClockRollback) ||
		errors.Is(ErrInvalidConfig, ErrClockRollback) {
		return errors.New("sentinel errors are not distinct")
	}
	if _, e := New(0, rate); !errors.Is(e, ErrInvalidConfig) {
		return fmt.Errorf("New(0,..) want ErrInvalidConfig, got %v", e)
	}
	return nil
}
