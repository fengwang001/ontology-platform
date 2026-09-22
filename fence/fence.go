// Package fence 提供围栏令牌（fencing token）的分配与校验。
//
// 令牌严格单调递增；每个 Fence 实例维护一个「已见最大值」水位，
// 小于水位的写入一律被拒绝。Fence 是并发安全的。
package fence

import (
	"errors"
	"fmt"
	"sync"
)

// Token 是围栏令牌，0 表示「无令牌」的零值，合法令牌从 1 开始。
type Token uint64

// ErrStaleToken 表示写入携带的令牌小于当前水位，可通过 errors.Is 判定。
var ErrStaleToken = errors.New("fence: stale token")

// StaleError 描述一次被水位拒绝的写入，携带令牌与当前水位。
type StaleError struct {
	Token     Token
	Watermark Token
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("fence: token %d is stale, watermark is %d", e.Token, e.Watermark)
}

// Is 使 errors.Is(err, ErrStaleToken) 成立。
func (e *StaleError) Is(target error) bool { return target == ErrStaleToken }

// Fence 是单个资源的令牌分配器与水位校验器。
type Fence struct {
	mu   sync.Mutex
	high Token // 已见最大值：既被 Issue 抬高，也被 Check 抬高
}

// New 返回一个水位为 0 的 Fence。
func New() *Fence { return &Fence{} }

// Issue 分配下一个令牌，保证严格大于此前分配过的所有令牌。
func (f *Fence) Issue() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.high++
	return f.high
}

// Check 校验写入令牌：小于当前水位则返回 *StaleError；
// 否则接受并把水位抬升到 token（相等时水位不变）。
func (f *Fence) Check(token Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if token < f.high {
		return &StaleError{Token: token, Watermark: f.high}
	}
	f.high = token
	return nil
}

// Max 返回当前已见最大值（水位）。
func (f *Fence) Max() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.high
}
