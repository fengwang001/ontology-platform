package shard

import (
	"testing"

	"ontology/internal/route"
)

// TestProbeCountConstant 白盒直接读非导出字段 probes：分区数从 100 到
// 10000 跨三个数量级，Get 为定位 key 检查的分区个数恒为 1（算术直达），
// 不随 m 线性增长。包外无法经任何导出途径读到 probes。
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(m)
		s.Put("k", "V")
		if v, ok := s.Get("k"); !ok || v != "V" {
			t.Fatalf("m=%d Get(k)=(%q,%v)", m, v, ok)
		}
		if got := s.probes.Load(); got != 1 {
			t.Fatalf("m=%d probes=%d, want 1 (constant in m)", m, got)
		}
		home := route.Home("k", m)
		if _, ok := s.At(home, "k"); !ok {
			t.Fatalf("m=%d At(home,k) miss", m)
		}
		if got := s.probes.Load(); got != 1 {
			t.Fatalf("m=%d probes after At=%d, want 1", m, got)
		}
	}
}

// TestSingleResidence 钉住不变量 1：任意 Put/Rebalance 序列后，每个 key
// 全库恰好一份，且就在 route.Home(key, N) 分区。
func TestSingleResidence(t *testing.T) {
	cases := []struct {
		name string
		n    int
		puts [][2]string
		news []int
	}{
		{"grow 2->3", 2, [][2]string{{"a", "A"}, {"b", "B"}, {"c", "C"}, {"d", "D"}, {"e", "E"}}, []int{3}},
		{"shrink 4->2", 4, [][2]string{{"a", "A"}, {"b", "B"}, {"cc", "CC"}}, []int{2}},
		{"oscillate", 3, [][2]string{{"a", "A"}, {"bb", "BB"}, {"xyz", "XYZ"}}, []int{1, 5, 2, 7, 3}},
		{"same n no-op", 2, [][2]string{{"a", "A"}}, []int{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.n)
			for _, kv := range tc.puts {
				s.Put(kv[0], kv[1])
			}
			for _, newN := range tc.news {
				s.Rebalance(newN)
			}
			dump := s.Dump()
			count := map[string]int{}
			values := map[string]string{}
			for p, m := range dump {
				for k, v := range m {
					count[k]++
					values[k] = v
					if p != route.Home(k, len(dump)) {
						t.Fatalf("%q resides p=%d but home=%d", k, p, route.Home(k, len(dump)))
					}
				}
			}
			for _, kv := range tc.puts {
				if count[kv[0]] != 1 {
					t.Fatalf("%q residence=%d, want exactly 1", kv[0], count[kv[0]])
				}
				if values[kv[0]] != kv[1] {
					t.Fatalf("%q value=%q want %q", kv[0], values[kv[0]], kv[1])
				}
			}
		})
	}
}
