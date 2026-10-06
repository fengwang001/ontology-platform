package cors

import (
	"fmt"
	"testing"
)

// BenchmarkClassifyConfigScale 验证简单/预检判定开销不随安全头集合之外
// 的配置规模（安全方法数、安全响应头数）增长：
// go test ./cors -run=NONE -bench=ClassifyConfigScale
func BenchmarkClassifyConfigScale(b *testing.B) {
	for _, extra := range []int{0, 4096} {
		cfg := baseConfig()
		for i := 0; i < extra; i++ {
			cfg.SafeMethods = append(cfg.SafeMethods, fmt.Sprintf("M%04d", i))
			cfg.SafeResponseHeaders = append(cfg.SafeResponseHeaders, fmt.Sprintf("x-r%04d", i))
		}
		eng, err := NewEngine(cfg)
		if err != nil {
			b.Fatalf("NewEngine: %v", err)
		}
		r := req("https://a", "https://b", "GET", []Header{{Name: "accept", Value: "abc"}}, false)
		b.Run(fmt.Sprintf("extra-config=%d", extra), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				d, err := eng.Decide(r)
				if err != nil || d.Kind != DecisionSimple {
					b.Fatalf("decide: %v %v", d, err)
				}
			}
		})
	}
}

// BenchmarkCacheHitScale 验证缓存命中判定开销不随缓存条目总数增长：
// go test ./cors -run=NONE -bench=CacheHitScale
func BenchmarkCacheHitScale(b *testing.B) {
	for _, size := range []int{16, 1 << 16} {
		cfg := baseConfig()
		cfg.Capacity = size
		cfg.DefaultMaxAge = 1 << 60
		cfg.MaxMaxAge = 1 << 60
		eng, err := NewEngine(cfg)
		if err != nil {
			b.Fatalf("NewEngine: %v", err)
		}
		for i := 0; i < size-1; i++ {
			r := req("https://a", fmt.Sprintf("https://t%d", i), "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
			if err := eng.SubmitPreflight(r, PreflightResponse{
				AllowOrigin:  "https://a",
				AllowMethods: []string{"PUT"},
				AllowHeaders: []string{"x-a"},
			}); err != nil {
				b.Fatalf("fill: %v", err)
			}
		}
		hit := req("https://a", "https://t0", "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
		b.Run(fmt.Sprintf("entries=%d", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				d, err := eng.Decide(hit)
				if err != nil || d.Kind != DecisionCacheHit {
					b.Fatalf("decide: %v %v", d, err)
				}
			}
		})
	}
}
