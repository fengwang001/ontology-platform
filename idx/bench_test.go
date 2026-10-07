package idx

import (
	"fmt"
	"math/rand"
	"testing"
)

// BenchmarkQueryScaling 独立验证"查询开销不随累计处理的事件总量线性
// 增长"：在固定对象数（1000）与取值基数（100）下，分别累计处理
// 1e3/1e4/1e5/1e6 条事件后测量单次 Query 耗时。若查询耗时随事件量
// 线性增长，各子基准的 ns/op 将呈数量级差异；预期结果是大致持平。
//
// 运行：go test -bench=QueryScaling -benchtime=1000x ./idx/
func BenchmarkQueryScaling(b *testing.B) {
	for _, events := range []int{1_000, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("events=%d", events), func(b *testing.B) {
			s, err := NewStore(NewTypeRegistry(activeProps()), "color")
			if err != nil {
				b.Fatal(err)
			}
			rng := rand.New(rand.NewSource(7))
			for i := 0; i < events; i++ {
				obj := fmt.Sprintf("o%d", rng.Intn(1000))
				_ = s.Ingest(Event{
					ID:       EventID(fmt.Sprintf("%s/%d", obj, i)),
					ObjectID: obj,
					Property: "color",
					Value:    fmt.Sprintf("v%d", rng.Intn(100)),
					Version:  uint64(i),
				})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Query("v42"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkIngest 测量单条事件的增量维护开销。
func BenchmarkIngest(b *testing.B) {
	s, err := NewStore(NewTypeRegistry(activeProps()), "color")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obj := fmt.Sprintf("o%d", i%1000)
		_ = s.Ingest(Event{
			ID:       EventID(fmt.Sprintf("%s/%d", obj, i)),
			ObjectID: obj,
			Property: "color",
			Value:    "v1",
			Version:  uint64(i),
		})
	}
}
