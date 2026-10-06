package rollout

import (
	"fmt"
	"testing"
)

// BenchmarkRouteBoundedByP 验证路由开销只随粘性上限 P 变化，
// 而与“历史请求总数”无关：历史请求由观测计数器承担（O(1)），
// 路由只触及 map 与至多 P 个节点的链表。
func BenchmarkRouteBoundedByP(b *testing.B) {
	for _, p := range []int{16, 256, 4096} {
		cfg := Config{
			Ratios: []int{5000, 10000}, DwellMs: 0, MinCanary: 1,
			ToleranceBP: 0, MaxFailures: 1, StickyMs: 1 << 40, MaxSticky: p,
		}
		s, _ := New(cfg)
		if err := s.Start(0); err != nil {
			b.Fatal(err)
		}
		// 先制造“海量历史请求”，但只有 P 条粘性留存。
		for i := 0; i < p; i++ {
			if _, err := s.Route(fmt.Sprintf("seed-%d", i), 1); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("P=%d", p), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// 常驻 P 条记录后反复路由少量热标识，时间戳恒为 1（允许相等）。
				id := fmt.Sprintf("hot-%d", i%p)
				if _, err := s.Route(id, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkObserve(b *testing.B) {
	cfg := Config{
		Ratios: []int{5000}, DwellMs: 0, MinCanary: 1,
		ToleranceBP: 0, MaxFailures: 1,
	}
	s, _ := New(cfg)
	_ = s.Start(0)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := s.Observe(VersionCanary, i%7 != 0, 1); err != nil {
			b.Fatal(err)
		}
	}
}
