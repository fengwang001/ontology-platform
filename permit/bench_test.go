package permit

import (
	"fmt"
	"testing"
)

// BenchmarkProgressScaling 证明单件查询/派生开销只与该件环节数相关，
// 与系统内同时在办的许可总数无关：随许可总数扩大，单件 Progress 用时应基本恒定。
func BenchmarkProgressScaling(b *testing.B) {
	for _, total := range []int{100, 1000, 4000} {
		b.Run(fmt.Sprintf("cases=%d", total), func(b *testing.B) {
			svc := NewService(nil)
			tp := &PermitType{ID: "P", Stages: []StageDef{
				{ID: "A", Department: "da", DueWorkdays: 5},
				{ID: "B", Department: "db", DueWorkdays: 5, Prereqs: []string{"A"}},
				{ID: "C", Department: "dc", DueWorkdays: 5, Prereqs: []string{"B"}},
			}}
			if err := svc.RegisterType(tp); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < total; i++ {
				if err := svc.Accept(1, fmt.Sprintf("c%d", i), "P"); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := svc.Progress(20, "c0"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
