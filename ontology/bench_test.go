package ontology

import (
	"context"
	"testing"
)

// BenchmarkPreemptionCheckDoesNotGrowWithContenders 验证抢占判定开销
// 不随并发竞争同一实例的动作总数增长：抢占与否由一次 O(1) 的
// 版本 + 高水位比较决定，与竞争者数量无关。
//
// 运行：go test -bench=Preemption -benchmem ./ontology
func BenchmarkPreemptionCheckDoesNotGrowWithContenders(b *testing.B) {
	for _, contenders := range []int{1, 8, 64, 512, 4096} {
		b.Run("n="+itoaBench(contenders), func(b *testing.B) {
			exec := NewExecutor(nil)
			// 预置：一个最高权限写入先生效，建立高水位。
			high := counterOp(999, Privilege(100), 1, 1)
			exec.Run(context.Background(), high)

			// 其余竞争者不断在真实并发下尝试（预算为 1，大多被抢占）。
			ops := make([]Op, contenders)
			for i := range ops {
				ops[i] = counterOp(i+1, Privilege(1), 1, 1)
			}
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				op := ops[k%len(ops)]
				op.ID = k + 1 // 避免结果聚合到同一 ID（仅为独立计时）
				exec.Run(context.Background(), op)
			}
		})
	}
}

func itoaBench(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
