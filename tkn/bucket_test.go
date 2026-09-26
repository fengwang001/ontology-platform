package tkn

import "testing"

// TestNewStartsFull：新建桶初始令牌数等于容量。
func TestNewStartsFull(t *testing.T) {
	cases := []struct {
		capacity, rate int64
	}{
		{1, 1}, {20, 2}, {100, 7},
	}
	for _, c := range cases {
		if b := New(c.capacity, c.rate); b.Tokens() != c.capacity {
			t.Errorf("New(%d,%d).Tokens()=%d, want %d", c.capacity, c.rate, b.Tokens(), c.capacity)
		}
	}
}

// TestRefill 表驱动：补令牌用一次乘法并按容量封顶；elapsed<=0 不补。
func TestRefill(t *testing.T) {
	cases := []struct {
		name                 string
		capacity, rate       int64
		start, elapsed, want int64
	}{
		{"partial", 20, 2, 5, 3, 11},
		{"clamp-to-cap", 20, 2, 7, 11, 20}, // 7+22 封顶
		{"full-stays-full", 20, 2, 20, 100, 20},
		{"zero-elapsed", 20, 2, 5, 0, 5},
		{"neg-elapsed-noop", 20, 2, 5, -3, 5},
		{"rate-one", 10, 1, 0, 4, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := New(c.capacity, c.rate)
			b.tokens = c.start
			b.Refill(c.elapsed)
			if b.Tokens() != c.want {
				t.Errorf("tokens=%d, want %d", b.Tokens(), c.want)
			}
		})
	}
}

// TestTryConsume 表驱动：足够则扣减，不足则不变。
func TestTryConsume(t *testing.T) {
	cases := []struct {
		start, need int64
		wantAllow   bool
		wantTokens  int64
	}{
		{20, 15, true, 5},
		{5, 8, false, 5},
		{1, 1, true, 0},
		{0, 1, false, 0},
		{11, 12, false, 11},
	}
	for _, c := range cases {
		b := New(20, 2)
		b.tokens = c.start
		if got := b.TryConsume(c.need); got != c.wantAllow || b.Tokens() != c.wantTokens {
			t.Errorf("start=%d need=%d: got (%v,%d), want (%v,%d)",
				c.start, c.need, got, b.Tokens(), c.wantAllow, c.wantTokens)
		}
	}
}

// TestRefillIsO1：补令牌总量 m 从 100 到 10000 多档，令 elapsed*rate==m。
// 正确实现是一次乘法，refillSteps 恒为 0——不得随 m 线性增长。
func TestRefillIsO1(t *testing.T) {
	for _, m := range []int64{100, 1000, 2500, 5000, 10000} {
		b := New(m, 1)  // 容量至少容得下 m，避免封顶干扰
		b.TryConsume(m) // 抽干到 0
		if b.Tokens() != 0 {
			t.Fatalf("m=%d: drain failed, tokens=%d", m, b.Tokens())
		}
		b.Refill(m) // elapsed*rate == m
		if b.Tokens() != m {
			t.Errorf("m=%d: tokens=%d, want %d", m, b.Tokens(), m)
		}
		if b.refillSteps != 0 {
			t.Errorf("m=%d: refillSteps=%d, want 0 (O(1) multiply, no per-token loop)", m, b.refillSteps)
		}
		// 紧跟一个 need 极小的消费，不应产生任何逐令牌递增。
		if !b.TryConsume(1) || b.Tokens() != m-1 || b.refillSteps != 0 {
			t.Errorf("m=%d: post-consume state wrong: tokens=%d steps=%d", m, b.Tokens(), b.refillSteps)
		}
	}
}
