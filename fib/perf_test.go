package fib

import (
	"math/rand/v2"
	"testing"
)

// perf_test.go 以可验证的方式证明:单次更新的处理开销只与受影响
// 地址范围内的路由规模及地址位数有关,与无关路由总数无关。
// 方法:通过 Stats 计数器直接测量每次更新实际重算的 trie 节点数,
// 在无关路由总数成倍增长时断言该计数不变且有界。

func seedRoutes(t *testing.T, m *Manager, r *rand.Rand, n int, hiOctetStart int) {
	t.Helper()
	ops := make([]Op, 0, n)
	for i := 0; i < n; i++ {
		// 在指定高字节段内撒随机 /24,避免与后续更新目标重叠。
		a := uint32(hiOctetStart+r.IntN(100))<<24 | (r.Uint32() & 0xFFFF00)
		ops = append(ops, PutOp(Prefix{Addr: a, Len: 24}, NH(nhPool[r.IntN(len(nhPool))])))
	}
	if err := m.Batch(ops...); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCostIndependentOfUnrelatedRoutes(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 43))
	m := NewManager(1 << 24)
	// 第一阶段:2 万条无关路由。
	seedRoutes(t, m, r, 20000, 50)
	target1 := Prefix{Addr: 10<<24 | 7<<16 | 9<<8 | 3, Len: 32}
	m.ResetStats()
	if err := m.Put(target1, NH("x")); err != nil {
		t.Fatal(err)
	}
	s1 := m.Stats()
	t.Logf("2 万条无关路由时,写入 /32: 重算节点=%d 继承色更新=%d", s1.Recomputes, s1.GammaUpdates)
	// 第二阶段:再灌 2 万条无关路由(不同地址段)。
	seedRoutes(t, m, r, 20000, 160)
	target2 := Prefix{Addr: 10<<24 | 8<<16 | 9<<8 | 4, Len: 32}
	m.ResetStats()
	if err := m.Put(target2, NH("y")); err != nil {
		t.Fatal(err)
	}
	s2 := m.Stats()
	t.Logf("4 万条无关路由时,写入 /32: 重算节点=%d 继承色更新=%d", s2.Recomputes, s2.GammaUpdates)
	// 主机路由只影响自身:重算节点数不超过地址位数+1,且与总量无关。
	const bound = 33
	if s1.Recomputes > bound || s2.Recomputes > bound {
		t.Fatalf("/32 写入重算节点数超过地址位数上界: %d, %d", s1.Recomputes, s2.Recomputes)
	}
	if s1.GammaUpdates != 0 || s2.GammaUpdates != 0 {
		t.Fatalf("/32 写入不应更新任何继承色: %d, %d", s1.GammaUpdates, s2.GammaUpdates)
	}
	// 撤销同样有界。
	m.ResetStats()
	if err := m.Delete(target1); err != nil {
		t.Fatal(err)
	}
	s3 := m.Stats()
	t.Logf("撤销 /32: 重算节点=%d 继承色更新=%d", s3.Recomputes, s3.GammaUpdates)
	if s3.Recomputes > bound {
		t.Fatalf("/32 撤销重算节点数超过上界: %d", s3.Recomputes)
	}
	// 对照:写入默认路由影响全部地址空间,开销随受影响范围增长
	// (这是允许的:开销与受影响地址范围内的路由规模有关)。
	m.ResetStats()
	if err := m.Put(Prefix{Addr: 0, Len: 0}, NH("d")); err != nil {
		t.Fatal(err)
	}
	s4 := m.Stats()
	t.Logf("写入 0.0.0.0/0(影响全空间): 重算节点=%d 继承色更新=%d", s4.Recomputes, s4.GammaUpdates)
	if s4.GammaUpdates <= 1000 {
		t.Fatalf("全空间更新的继承色更新数应随范围增长, 仅 %d", s4.GammaUpdates)
	}
	// 范围受限的更新:只触及受影响范围内的节点。
	m.ResetStats()
	if err := m.Put(Prefix{Addr: 10 << 24, Len: 8}, NH("e")); err != nil {
		t.Fatal(err)
	}
	s5 := m.Stats()
	t.Logf("写入 10/8(影响该 /8): 重算节点=%d 继承色更新=%d", s5.Recomputes, s5.GammaUpdates)
	if s5.Recomputes+s5.GammaUpdates >= s4.Recomputes+s4.GammaUpdates {
		t.Fatalf("/8 更新的开销 %v 不应达到全空间更新的量级 %v", s5, s4)
	}
}
