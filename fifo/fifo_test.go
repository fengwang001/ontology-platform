package fifo

import "testing"

// TestCascadeProbeLinear 钉住第四节：m 连发级联中，为判断「缓冲是否含 next」
// 检查过的条目数不超过 m+常数（队首指针每步 O(1) 定位），
// 而不是每放行一次就 O(m) 扫描导致的 O(m^2)。probes 为非导出字段，无公开读法。
func TestCascadeProbeLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		b := New(m + 1)
		for s := int64(2); s <= int64(m)+1; s++ { // next+1..next+m 全部提前到达
			if _, err := b.Feed(s); err != nil {
				t.Fatalf("m=%d seed %d: %v", m, s, err)
			}
		}
		em, err := b.Feed(1) // 触发一次完整 m+1 连发
		if err != nil || len(em) != m+1 {
			t.Fatalf("m=%d cascade: emit=%d err=%v", m, len(em), err)
		}
		if b.probes < int64(m) {
			t.Fatalf("m=%d probes=%d < m: 计数器未生效", m, b.probes)
		}
		if b.probes > int64(m)+8 { // m + 常数；O(m^2) 实现会到约 m^2/2
			t.Fatalf("m=%d probes=%d > m+8: 疑似每步线性扫描", m, b.probes)
		}
	}
}

// TestCascadeStopsAtHole 探针在遇空洞时多查一条即停；下一级联重新计数。
func TestCascadeStopsAtHole(t *testing.T) {
	b := New(3)
	if _, err := b.Feed(2); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Feed(4); err != nil {
		t.Fatal(err)
	}
	if em, _ := b.Feed(1); len(em) != 2 || em[1] != 2 || b.probes != 2 || b.Next() != 3 {
		t.Fatalf("hole cascade: em=%v probes=%d next=%d", em, b.probes, b.Next())
	}
	if em, _ := b.Feed(3); len(em) != 2 || em[0] != 3 || em[1] != 4 || b.probes != 1 {
		t.Fatalf("second cascade: em=%v probes=%d", em, b.probes)
	}
}
