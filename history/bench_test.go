package history

// 性能可验证性：
// 1. BenchmarkTraverseSameDoc 证明遍历定位目标条目与判定同文档的开销不随历史长度增长
//    （切片下标 + 文档标识比较，均为 O(1)）。
// 2. BenchmarkCacheEviction 证明容量淘汰选出被驱逐文档的开销不随缓存文档数线性增长
//    （最小堆，O(log n)）。
// 运行：go test ./history -bench . -benchmem

import (
	"fmt"
	"testing"
)

func benchmarkTraverseSameDoc(b *testing.B, n int) {
	k, err := NewKernel(Config{Capacity: 4})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := k.Navigate("u0", 0, false); err != nil {
		b.Fatal(err)
	}
	for i := 1; i < n; i++ {
		if _, err := k.Navigate(fmt.Sprintf("u0#f%d", i), i, true); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := k.TraverseSync(-1); r.Err != nil {
			b.Fatal(r.Err)
		}
		if r := k.TraverseSync(1); r.Err != nil {
			b.Fatal(r.Err)
		}
	}
}

func BenchmarkTraverseSameDoc(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			benchmarkTraverseSameDoc(b, n)
		})
	}
}

func benchmarkCacheEviction(b *testing.B, capacity int) {
	k, err := NewKernel(Config{Capacity: capacity})
	if err != nil {
		b.Fatal(err)
	}
	// 填满缓存：capacity 个文档在缓存中，1 个为当前文档。
	for i := 0; i <= capacity; i++ {
		if _, err := k.Navigate(fmt.Sprintf("u%d", i), i, false); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 每次跨文档导航都会让当前文档入缓存并挤出最早者。
		if _, err := k.Navigate(fmt.Sprintf("w%d", i), i, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCacheEviction(b *testing.B) {
	for _, c := range []int{100, 10_000, 100_000} {
		b.Run(fmt.Sprintf("cache=%d", c), func(b *testing.B) {
			benchmarkCacheEviction(b, c)
		})
	}
}
