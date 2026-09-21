package retry

import (
	"testing"
	"time"
)

// 回归：旧抖动公式 1+r*p/100 只向上偏，rnd 恒 0 时取到的
// 恰是基准值而非下界 1-p/100，下侧区间永远够不到。
func TestJitterSymmetricAroundBase(t *testing.T) {
	p := Policy{Base: time.Second, Factor: 1, JitterPct: 25}
	lo := p.delay(1, func() float64 { return 0 })
	if lo != 750*time.Millisecond {
		t.Fatalf("rnd=0: delay = %v, want 750ms (base*(1-p/100))", lo)
	}
	hi := p.delay(1, func() float64 { return 0.999 })
	if hi <= time.Second || hi >= 1250*time.Millisecond {
		t.Fatalf("rnd~1: delay = %v, want in (1s, 1.25s)", hi)
	}
	if hi < 1249*time.Millisecond {
		t.Fatalf("rnd~1: delay = %v, want close to 1.25s", hi)
	}
}
