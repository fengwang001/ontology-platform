package sdot

import (
	"math/rand"
	"testing"

	"ontology/spv"
)

// randomVec 用随机分布的 10 个唯一非零条目构造向量（下标在 [0,m) 内）。
func randomVec(m int, seed int64) *spv.Vec {
	r := rand.New(rand.NewSource(seed))
	v := spv.New(m)
	for k, idx := range r.Perm(m)[:10] {
		if err := v.Set(idx, float64(k+1)+float64(seed%7)); err != nil { // 恒非零
			panic(err)
		}
	}
	return v
}

// onceCount 返回一次 Dot 在共享计数器上新增的访问条目数。
func onceCount(a, b *spv.Vec) int64 {
	before := accessedCount()
	Dot(a, b)
	return accessedCount() - before
}

// TestAccessCountIndependentOfM 钉住不变量 3：两个各含 10 个非零条目的向量，
// 稠密维度 m 取 100/1000/10000，单次 Dot 访问条目数恒为 20，不随 m 增长。
func TestAccessCountIndependentOfM(t *testing.T) {
	for _, c := range []struct {
		m    int
		seed int64
	}{{100, 1}, {1000, 2}, {10000, 3}} {
		got := onceCount(randomVec(c.m, c.seed), randomVec(c.m, c.seed+100))
		if got != 20 { // 必须恒等于两向量非零条目数之和，不超过 20 且不随 m 增长
			t.Fatalf("m=%d: accessed %d, want 20", c.m, got)
		}
	}
}

// TestAccessCountExact 单次 Dot 的访问数严格等于两侧非零条目数之和。
func TestAccessCountExact(t *testing.T) {
	for _, c := range []struct {
		na, nb, m int
	}{{0, 0, 16}, {1, 5, 16}, {7, 3, 64}, {10, 10, 100000}} {
		a, b := spv.New(c.m), spv.New(c.m)
		for k := 0; k < c.na; k++ {
			a.Set(k, float64(k+1))
		}
		for k := 0; k < c.nb; k++ {
			b.Set(k*2, float64(k+1))
		}
		if got := onceCount(a, b); got != int64(c.na+c.nb) {
			t.Fatalf("na=%d nb=%d: accessed %d, want %d", c.na, c.nb, got, c.na+c.nb)
		}
	}
}
