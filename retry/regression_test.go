package retry

import (
	"errors"
	"testing"
	"time"
)

// 回归：Cap 小于 Base 时第一次等待就必须截断为 Cap。
// 根因：baseDelay 只在乘法循环内部检查 Cap，k=1 时循环不执行，
// 且乘完最后一轮后也不再检查，导致首等及超 Cap 的值漏截断。
func TestCapBelowBaseTruncatesFirstWait(t *testing.T) {
	p := Policy{Base: 500 * time.Millisecond, Factor: 2, Cap: 100 * time.Millisecond}
	for k := 1; k <= 3; k++ {
		if got := p.baseDelay(k); got != 100*time.Millisecond {
			t.Fatalf("baseDelay(%d) = %v, want 100ms (Cap < Base)", k, got)
		}
	}
}

// 回归：rnd 恒返回 0 时间隔必须取到下界 base*(1-p/100)，严格小于基准值。
// 根因：delay 的抖动因子写成 1+r*p/100，只向上偏，丢失了 -p 一侧。
func TestJitterReachesLowerBound(t *testing.T) {
	p := Policy{Base: 200 * time.Millisecond, Factor: 1, JitterPct: 50}
	if got := p.delay(1, func() float64 { return 0 }); got != 100*time.Millisecond {
		t.Fatalf("delay with rnd=0 = %v, want 100ms (base*(1-p/100))", got)
	}
}

// 回归：永久错误中止时，返回错误必须同时 Is(ErrAborted) 且 Is(原错误)。
// 根因：Do 只包了 ErrAborted（fmt.Errorf("%w", ErrAborted)），把被 Permanent
// 包裹的原错误整条丢弃，errors.Is(err, orig) 永远为 false。
func TestPermanentAbortWrappedOriginalPreserved(t *testing.T) {
	orig := errors.New("disk gone")
	r := New(Policy{MaxAttempts: 3, Base: time.Millisecond}, func(time.Duration) {}, nil)
	_, err := r.Do(func(int) error { return Permanent(orig) })
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want Is(ErrAborted)", err)
	}
	if !errors.Is(err, orig) {
		t.Fatalf("err = %v, want Is(orig) — original error dropped", err)
	}
}
