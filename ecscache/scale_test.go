package ecscache

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// 本文件验证命中判定开销只随地址位数与同名字同类型条目数 K 变化，
// 不随缓存总条目数增长。实现依据：桶定位是哈希查找（O(1) 均摊），
// 桶内扫描至多 K 个条目，每个条目的覆盖判定为 O(地址字节数)。

// newScaleCache 构造一个含 total 个条目（分布在不同的名字桶中）的缓存，
// 并保证热键 "hot.example"/1 下有一个 /0 条目。
func newScaleCache(total int) *Cache {
	c := New(4, time.Now, ResolverFunc(func(context.Context, Request) (Response, error) {
		return Response{Result: recs("unused"), TTL: 0, Scope: 0}, nil
	}))
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeLocked(now, "hot.example", 1, FamilyIPv4, v4(10, 0, 0, 1), 0, recs("hot"), MaxTTLSeconds)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("filler-%d.example", i)
		addr := v4(byte(i>>24), byte(i>>16), byte(i>>8), byte(i))
		c.storeLocked(now, name, 1, FamilyIPv4, addr, 24, recs("f"), MaxTTLSeconds)
	}
	return c
}

// measureHit 对热键执行 rounds×perRound 次命中查询，返回单轮最短耗时。
func measureHit(c *Cache, rounds, perRound int) time.Duration {
	q := Query{Name: "hot.example", Type: 1, ClientAddress: v4(9, 9, 9, 9), SourcePrefixLen: 0}
	if _, err := c.Resolve(context.Background(), q); err != nil {
		panic(err)
	}
	best := time.Duration(1<<63 - 1)
	for r := 0; r < rounds; r++ {
		start := time.Now()
		for i := 0; i < perRound; i++ {
			if _, err := c.Resolve(context.Background(), q); err != nil {
				panic(err)
			}
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

// TestHitCostIndependentOfTotalEntries 在总条目数 1e3 / 1e4 / 1e5 下测量
// 同一热键的命中耗时，打印对照表并做宽松断言（100 倍数据量下耗时不超
// 10 倍），用于可验证地证明命中开销与总条目数无关。
func TestHitCostIndependentOfTotalEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scaling measurement in -short mode")
	}
	sizes := []int{1_000, 10_000, 100_000}
	durs := make([]time.Duration, len(sizes))
	for i, total := range sizes {
		c := newScaleCache(total)
		durs[i] = measureHit(c, 5, 2000)
		t.Logf("total_entries=%-7d hit_latency=%.1f ns/op", total, float64(durs[i])/2000)
	}
	// 宽松上界：数量级增长 100 倍时，命中耗时增长不得超过 10 倍。
	if durs[2] > 10*durs[0] {
		t.Fatalf("hit cost grows with total entries: %v (1e3) -> %v (1e5)", durs[0], durs[2])
	}
}

// BenchmarkHit 分别在不同总条目数下测量命中开销，供 `go test -bench` 使用。
func BenchmarkHit(b *testing.B) {
	for _, total := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("total=%d", total), func(b *testing.B) {
			c := newScaleCache(total)
			q := Query{Name: "hot.example", Type: 1, ClientAddress: v4(9, 9, 9, 9), SourcePrefixLen: 0}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.Resolve(context.Background(), q); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
