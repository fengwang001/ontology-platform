package retry

import (
	"testing"
	"time"
)

// 回归：Cap 过去只在乘法循环内检查，第一次等待（k=1）以及
// Factor<=1 时完全漏截断，且截断发生在乘法之前而非之后。
func TestCapClampsFromFirstWait(t *testing.T) {
	small := Policy{Base: 500 * time.Millisecond, Factor: 2, Cap: 100 * time.Millisecond}
	for k := 1; k <= 3; k++ {
		if got := small.baseDelay(k); got != 100*time.Millisecond {
			t.Fatalf("Cap<Base: delay(%d) = %v, want 100ms", k, got)
		}
	}
	flat := Policy{Base: 500 * time.Millisecond, Factor: 1, Cap: 100 * time.Millisecond}
	if got := flat.baseDelay(1); got != 100*time.Millisecond {
		t.Fatalf("Factor<=1 with Cap<Base: delay(1) = %v, want 100ms", got)
	}
	grow := Policy{Base: 100 * time.Millisecond, Factor: 3, Cap: 250 * time.Millisecond}
	if got := grow.baseDelay(2); got != 250*time.Millisecond {
		t.Fatalf("clamp after multiply: delay(2) = %v, want 250ms", got)
	}
}
