package retry

import (
	"errors"
	"testing"
	"time"
)

// 回归：旧代码只 %w 了 ErrAborted，把 fn 返回的 Permanent 原错误
// 整个丢掉，调用方无法再用 errors.Is 判定原始原因。
func TestAbortedWrapsBothSentinelAndCause(t *testing.T) {
	orig := errors.New("disk gone")
	r := New(Policy{MaxAttempts: 5, Base: time.Millisecond},
		func(time.Duration) {}, nil)
	n, err := r.Do(func(int) error { return Permanent(orig) })
	if n != 1 {
		t.Fatalf("attempts = %d, want 1 (abort on first permanent error)", n)
	}
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want Is(ErrAborted)", err)
	}
	if !errors.Is(err, orig) {
		t.Fatalf("err = %v, want Is(orig) to reach the wrapped cause", err)
	}
}
