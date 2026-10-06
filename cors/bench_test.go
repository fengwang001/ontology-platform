package cors

import (
	"fmt"
	"testing"
)

// 性能证明一：简单与否判定的开销不随安全头集合之外的配置规模增长。
// 安全头数量从 10 增加到 10000，单次判定耗时应基本不变（哈希查找）。
func BenchmarkClassify(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("safeHeaders=%d", n), func(b *testing.B) {
			cfg := baseConfig()
			cfg.SafeHeaders = map[string]HeaderRule{}
			for i := 0; i < n; i++ {
				cfg.SafeHeaders[fmt.Sprintf("x-safe-%d", i)] = HeaderRule{MaxLen: 16}
			}
			k, err := NewKernel(cfg)
			if err != nil {
				b.Fatal(err)
			}
			req := Request{Origin: "https://a.example", Target: "https://b.example", Method: "GET"}
			for i := 0; i < 8; i++ {
				req.Headers = append(req.Headers, Header{
					Name:  fmt.Sprintf("x-safe-%d", n-1-i),
					Value: "v",
				})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := k.Decide(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// 性能证明二：缓存命中判定的开销不随缓存条目总数增长。
// 条目数从 8 增加到 4096，单次命中耗时应基本不变（哈希查找）。
func BenchmarkCacheHit(b *testing.B) {
	for _, n := range []int{8, 64, 512, 4096} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			cfg := baseConfig()
			cfg.MaxEntries = n + 1
			cfg.MaxAgeMax = 1 << 40
			k, err := NewKernel(cfg)
			if err != nil {
				b.Fatal(err)
			}
			var first Request
			for i := 0; i < n; i++ {
				req := Request{
					Origin:  fmt.Sprintf("https://o%d.example", i),
					Target:  "https://t.example",
					Method:  "PUT",
					Headers: []Header{{"X-A", "1"}},
				}
				if i == 0 {
					first = req
				}
				resp := allowPreflight(req.Origin, "PUT", "x-a", "1000000")
				if err := k.SubmitPreflightResponse(req, resp); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d, err := k.Decide(first)
				if err != nil {
					b.Fatal(err)
				}
				if !d.CacheHit {
					b.Fatal("want cache hit")
				}
			}
		})
	}
}
