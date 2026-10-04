package pool

import "testing"

func TestBasics(t *testing.T) {
	cases := []int{1, 2, 63, 64, 65, 127, 128, 1000, 100000}
	for _, h := range cases {
		p := New(h)
		if p.H() != h {
			t.Fatalf("H=%d want %d", p.H(), h)
		}
		if got, ok := p.FirstAvailable(); !ok || got != 1 {
			t.Fatalf("h=%d first=%d,%v want 1,true", h, got, ok)
		}
		if !p.Remove(1) {
			t.Fatalf("h=%d Remove(1) failed", h)
		}
		if h >= 2 {
			if got, ok := p.FirstAvailable(); !ok || got != 2 {
				t.Fatalf("h=%d after remove 1 first=%d,%v want 2", h, got, ok)
			}
		}
		if p.Available(1) || p.Remove(1) {
			t.Fatalf("h=%d hero 1 should be gone", h)
		}
		// 删除前缀后应直接跳到第一个未删英雄。
		for hero := 2; hero <= 100 && hero <= h; hero++ {
			p.Remove(hero)
		}
		if h > 100 {
			if got, ok := p.FirstAvailable(); !ok || got != 101 {
				t.Fatalf("h=%d first=%d,%v want 101", h, got, ok)
			}
		}
		if h > 100 {
			// Restore 后立即可用。
			p.Restore(5)
			if got, _ := p.FirstAvailable(); got != 5 {
				t.Fatalf("h=%d after restore 5 first=%d want 5", h, got)
			}
		}
	}
}

func TestDrain(t *testing.T) {
	for _, h := range []int{64, 65, 4096, 99999} {
		p := New(h)
		for hero := 1; hero <= h; hero++ {
			if !p.Remove(hero) {
				t.Fatalf("h=%d remove %d failed", h, hero)
			}
		}
		if _, ok := p.FirstAvailable(); ok {
			t.Fatalf("h=%d expected empty pool", h)
		}
		p.Restore(h)
		if got, ok := p.FirstAvailable(); !ok || got != h {
			t.Fatalf("h=%d after restore got %d,%v want %d", h, got, ok, h)
		}
	}
}

// TestTouchedBound 证明一次自动选人（预选可用性检查 + 最小可用查找）
// 读取的记录数不超过 40，且 H=64 与 H=65536 两档基本一致，不随 H 线性增长。
func TestTouchedBound(t *testing.T) {
	var perH []int
	for _, h := range []int{64, 65536} {
		p := New(h)
		// 模拟若干英雄已被禁/选。
		for _, hero := range []int{1, 2, 3, 5, 8, 13, 21, 34, 63, 64, 65, 100, 1000, 40000} {
			if hero <= h {
				p.Remove(hero)
			}
		}
		p.ResetTouched()
		_ = p.Available(5) // 预选英雄：已被占用，检查一次
		hero, ok := p.FirstAvailable()
		if !ok || hero != 4 {
			t.Fatalf("h=%d auto pick got %d,%v want 4", h, hero, ok)
		}
		n := p.Touched()
		perH = append(perH, n)
		t.Logf("H=%d touched=%d for one auto pick (preselect check + first-available)", h, n)
		if n > 40 {
			t.Fatalf("H=%d touched=%d exceeds 40", h, n)
		}
	}
	// H 相差 1024 倍，读取记录数只可能因分层位图层高（ceil(log64 H)）相差至多 1，
	// 且两档都不超过 6，证明复杂度随 log H 而非 H 线性增长。
	if perH[0] > 6 || perH[1] > 6 {
		t.Fatalf("touched too large: %v", perH)
	}
	if perH[1]-perH[0] > 1 || perH[0]-perH[1] > 1 {
		t.Fatalf("touched grows with H: %v", perH)
	}
}

func TestInvalidHero(t *testing.T) {
	p := New(10)
	for _, hero := range []int{0, -1, 11} {
		if p.Available(hero) {
			t.Fatalf("hero %d must be unavailable", hero)
		}
		if p.Remove(hero) {
			t.Fatalf("hero %d remove must fail", hero)
		}
	}
}
