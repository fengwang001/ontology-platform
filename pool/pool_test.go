package pool

import (
	"math/rand/v2"
	"testing"
)

func TestAvailableAndMin(t *testing.T) {
	p := New(10)
	if got := p.MinAvailable(); got != 1 {
		t.Fatalf("MinAvailable()=%d, want 1", got)
	}
	p.Ban(1)
	p.Pick(3)
	p.Ban(2)
	for _, h := range []int{1, 2, 3} {
		if p.Available(h) {
			t.Fatalf("Available(%d)=true, want false", h)
		}
	}
	if got := p.MinAvailable(); got != 4 {
		t.Fatalf("MinAvailable()=%d, want 4", got)
	}
	p.Pick(4)
	if got := p.MinAvailable(); got != 5 {
		t.Fatalf("MinAvailable()=%d, want 5", got)
	}
}

// TestTouchedBound 模拟一次自动选人（查预选可用性 + 找最小可用 + 选取），
// 证明读取的英雄池记录数不超过 40 且不随 H 线性增长。
func TestTouchedBound(t *testing.T) {
	deltas := map[int]int{}
	for _, h := range []int{64, 65536} {
		p := New(h)
		for hero := 1; hero <= 30; hero++ { // 删除数上界 2n+2(b1+b2)=30，构造最长链
			p.remove(hero)
		}
		p.touched = 0
		p.Available(7) // 预选英雄可用性检查
		m := p.MinAvailable()
		p.Pick(m)
		deltas[h] = p.touched
		if p.touched > 40 {
			t.Fatalf("H=%d: 一次自动选人读取 %d 条记录, 超过 40", h, p.touched)
		}
		if m := p.MinAvailable(); m != 32 { // 1..30 已删，31 刚被选走
			t.Fatalf("H=%d: MinAvailable()=%d, want 32", h, m)
		}
	}
	if deltas[64] != deltas[65536] {
		t.Fatalf("读取数随 H 增长: H=64 为 %d, H=65536 为 %d", deltas[64], deltas[65536])
	}
	t.Logf("touched: H=64 -> %d, H=65536 -> %d（上界 40）", deltas[64], deltas[65536])
}

// TestFindVsBrute 随机删除后与从 1 起逐个扫描的朴素结果对照。
func TestFindVsBrute(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 200; iter++ {
		h := 1 + rng.IntN(200)
		p := New(h)
		avail := make([]bool, h+1)
		for i := range avail {
			avail[i] = true
		}
		removed := 0
		for removed < h {
			hero := 1 + rng.IntN(h)
			if !avail[hero] {
				continue
			}
			avail[hero] = false
			p.remove(hero)
			removed++
			want := 0
			for x := 1; x <= h; x++ {
				if avail[x] {
					want = x
					break
				}
			}
			if want == 0 {
				break
			}
			if got := p.MinAvailable(); got != want {
				t.Fatalf("iter=%d h=%d: MinAvailable()=%d, want %d", iter, h, got, want)
			}
		}
	}
}
