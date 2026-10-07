package ontology

import (
	"fmt"
	"testing"
)

// 基准：呈现开销不随系统中策略总数增长。
// 运行 go test -bench=. 观察各规模下 ns/op 是否平稳。
func BenchmarkPresentScaling(b *testing.B) {
	for _, total := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("policies=%d", total), func(b *testing.B) {
			e := NewEngine(false)
			e.SetSchema(baseSchema())
			e.RegisterVisibility(VisibilityPolicy{ID: "v-ssn", Subject: "*", Attr: "ssn", Effect: Allow})
			e.RegisterMasking(MaskingPolicy{ID: "m-ssn", Subject: "*", Attr: "ssn", Strength: 1,
				Rule: MaskingRule{Kind: RuleHash}})
			for i := 0; i < total; i++ {
				attr := fmt.Sprintf("other-%d", i)
				e.RegisterMasking(MaskingPolicy{ID: "mx-" + attr, Subject: "*", Attr: attr,
					Strength: 1, Rule: MaskingRule{Kind: RuleRedact}})
			}
			inst := baseInstance()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e.Present("bench", inst)
			}
		})
	}
}
