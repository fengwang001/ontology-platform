package exports

import (
	"fmt"
	"testing"
)

// BenchmarkResolveScaling 验证单次解析开销不随键总数增长：
// 在不同规模的表上解析同一个通配请求，耗时应大致恒定。
// 运行: go test -bench=Scaling -benchmem ./exports/
func BenchmarkResolveScaling(b *testing.B) {
	for _, size := range []int{100, 1000, 10000, 100000} {
		entries := make([]Entry, 0, size+1)
		entries = append(entries, Entry{Key: "./target/*", Target: StringTarget("./out/*/x.js")})
		for i := 0; i < size; i++ {
			entries = append(entries, Entry{
				Key:    fmt.Sprintf("./noise/%d/*", i),
				Target: StringTarget("./noise/*"),
			})
		}
		r, err := NewResolver(entries)
		if err != nil {
			b.Fatalf("NewResolver: %v", err)
		}
		b.Run(fmt.Sprintf("keys=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := r.Resolve("./target/a/b", nil); err != nil {
					b.Fatalf("Resolve: %v", err)
				}
			}
		})
	}
}
