package retry

import (
	"testing"
	"time"
)

// 语义 2：JitterPct=0 时第 k 次等待为 Base*Factor^(k-1)， capped by Cap。
func TestBaseDelaySequence(t *testing.T) {
	p := Policy{Base: 100 * time.Millisecond, Factor: 3, Cap: 250 * time.Millisecond}
	want := []time.Duration{100, 250, 250, 250}
	for i, w := range want {
		if got := p.delay(i+1, nil); got != w*time.Millisecond {
			t.Fatalf("delay(%d) = %v, want %v", i+1, got, w*time.Millisecond)
		}
	}
}

// 语义 2：Factor<=1 时每次都是 Base；Cap<=0 表示不设上限。
func TestBaseDelayNoGrowthNoCap(t *testing.T) {
	flat := Policy{Base: 50 * time.Millisecond, Factor: 1}
	for k := 1; k <= 4; k++ {
		if got := flat.delay(k, nil); got != 50*time.Millisecond {
			t.Fatalf("flat delay(%d) = %v, want 50ms", k, got)
		}
	}
	uncapped := Policy{Base: time.Second, Factor: 2}
	want := []time.Duration{1, 2, 4, 8}
	for i, w := range want {
		if got := uncapped.delay(i+1, nil); got != w*time.Second {
			t.Fatalf("uncapped delay(%d) = %v, want %v", i+1, got, w*time.Second)
		}
	}
}

// 语义 3：抖动有界且每次等待只调用一次 rnd。
func TestJitterBoundsAndSingleRndCall(t *testing.T) {
	p := Policy{Base: 200 * time.Millisecond, Factor: 1, JitterPct: 50}
	seq := []float64{0, 0.5, 0.999}
	calls := 0
	rnd := func() float64 {
		v := seq[calls]
		calls++
		return v
	}
	lo := p.delay(1, rnd)
	mid := p.delay(2, rnd)
	hi := p.delay(3, rnd)
	if calls != 3 {
		t.Fatalf("rnd called %d times, want 3 (once per wait)", calls)
	}
	if lo != 100*time.Millisecond {
		t.Fatalf("lower bound = %v, want 100ms", lo)
	}
	if mid != 200*time.Millisecond {
		t.Fatalf("midpoint = %v, want 200ms", mid)
	}
	upper := 300 * time.Millisecond
	if hi <= mid || hi >= upper {
		t.Fatalf("upper jitter = %v, want in (200ms, 300ms)", hi)
	}
	if hi < 299*time.Millisecond {
		t.Fatalf("upper jitter = %v, want close to 300ms", hi)
	}
}

// 语义 3 边界：任意随机值下抖动都不越界。
func TestJitterNeverOutOfBounds(t *testing.T) {
	p := Policy{Base: time.Second, Factor: 1, JitterPct: 100}
	for _, r := range []float64{0, 0.25, 0.5, 0.75, 0.999999} {
		got := p.delay(1, func() float64 { return r })
		if got < 0 || got > 2*time.Second {
			t.Fatalf("jitter(%v) = %v, out of [0, 2s]", r, got)
		}
	}
}
