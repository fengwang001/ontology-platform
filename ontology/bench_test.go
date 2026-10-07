package ontology

import (
	"fmt"
	"testing"
)

// 以下基准是"判定开销不随实例总数/占用记录总数增长"的可验证证据：
// 在不同规模下测量拒绝判定的 ns/op，应近似为水平直线。
// 运行：go test -bench=Scaling -benchtime=1000x ./ontology/

func benchStore(b *testing.B, n int) *Store {
	s := NewStore()
	s.RegisterType(ObjectType{
		Name:       "Ticket",
		Properties: []PropertySpec{{Name: "title", Required: true}},
	})
	for i := 0; i < n; i++ {
		id := InstanceID(fmt.Sprintf("I%07d", i))
		if err := s.CreateInstance(id, "Ticket", map[string]PropertyValue{"title": "t"}); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

var scalingSizes = []int{1_000, 10_000, 100_000}

// 占用申请被拒绝（实例已被占用）的判定开销 vs 实例总数。
func BenchmarkAcquireRejectScaling(b *testing.B) {
	for _, n := range scalingSizes {
		b.Run(fmt.Sprintf("instances=%d", n), func(b *testing.B) {
			s := benchStore(b, n)
			if _, err := s.Acquire("A-holder", "I0000000"); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 每次都应被立即拒绝：RejectOccupiedByAction。
				if _, err := s.Acquire("A-other", "I0000000"); err == nil {
					b.Fatal("expected rejection")
				}
			}
		})
	}
}

// 占用期间普通更新被拒绝的判定开销 vs 实例总数。
func BenchmarkUpdateRejectOccupiedScaling(b *testing.B) {
	for _, n := range scalingSizes {
		b.Run(fmt.Sprintf("instances=%d", n), func(b *testing.B) {
			s := benchStore(b, n)
			if _, err := s.Acquire("A-holder", "I0000000"); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Update("c", "I0000000", 1,
					Mutation{Property: "title", Value: "x"}); err == nil {
					b.Fatal("expected rejection")
				}
			}
		})
	}
}

// 版本落后拒绝的判定开销 vs 实例总数与并存占用记录总数。
func BenchmarkUpdateVersionStaleScaling(b *testing.B) {
	for _, n := range scalingSizes {
		b.Run(fmt.Sprintf("instances=%d", n), func(b *testing.B) {
			s := benchStore(b, n)
			// 让系统中并存大量占用记录（占用除目标实例外的前一半实例）。
			for i := 1; i <= n/2; i++ {
				id := InstanceID(fmt.Sprintf("I%07d", i))
				if _, err := s.Acquire("A-holder", id); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 目标实例未被占用但版本已落后：RejectVersionStale。
				if _, err := s.Update("c", "I0000000", 999,
					Mutation{Property: "title", Value: "x"}); err == nil {
					b.Fatal("expected rejection")
				}
			}
		})
	}
}
