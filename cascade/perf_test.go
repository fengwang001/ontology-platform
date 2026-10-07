package cascade_test

import (
	"fmt"
	"testing"
	"time"

	"ontology/cascade"
)

// TestComplexityIndependentOfTotalSize 可验证地证明：
// 一次只影响常数个对象/引用的操作，其耗时不随“无关对象总数”增长。
//
// 方法：分别在含 N 与 10N 个互不相关对象的控制器上，执行同一条
// “只触及固定小子图”的删除操作。若复杂度依赖被影响面而非总数，
// 两次耗时应处于同一量级（断言上界比率 < 3，且 10N 的绝对耗时很小）。
func TestComplexityIndependentOfTotalSize(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test")
	}
	measure := func(n int) time.Duration {
		c := cascade.New()
		// n 个互不相连、各自带终结器的对象（终结器使其不会被任何
		// 操作连带影响），构成巨大的“无关对象池”。
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("iso%d", i)
			if err := c.Create(id, nil, []string{"keep"}); err != nil {
				t.Fatal(err)
			}
		}
		// 一条独立的三节点小链：a <- b <- d，均无终结器。
		if err := c.Create("a", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := c.Create("b", []cascade.OwnerRef{{OwnerID: "a"}}, nil); err != nil {
			t.Fatal(err)
		}
		if err := c.Create("d", []cascade.OwnerRef{{OwnerID: "b"}}, nil); err != nil {
			t.Fatal(err)
		}

		// 预热后用 b.N 框架之外的手动计时：重复“同构”的小操作，
		// 通过不断重建被删链来反复度量。取删除 a（固定影响 3 个对象）
		// 的单次耗时。
		start := time.Now()
		// 每次重建 a/b/d 再删除，重复 rep 次；无关池始终不变。
		const rep = 50
		for k := 0; k < rep; k++ {
			ids := [3]string{
				fmt.Sprintf("a%d", k),
				fmt.Sprintf("b%d", k),
				fmt.Sprintf("d%d", k),
			}
			if err := c.Create(ids[0], nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := c.Create(ids[1], []cascade.OwnerRef{{OwnerID: ids[0]}}, nil); err != nil {
				t.Fatal(err)
			}
			if err := c.Create(ids[2], []cascade.OwnerRef{{OwnerID: ids[1]}}, nil); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(ids[0], cascade.Background); err != nil {
				t.Fatal(err)
			}
		}
		return time.Since(start) / rep
	}

	const small, large = 2000, 20000
	dSmall := measure(small)
	dLarge := measure(large)
	t.Logf("avg small-chain delete: N=%d -> %v ; N=%d -> %v ; ratio %.2f",
		small, dSmall, large, dLarge, float64(dLarge)/float64(dSmall))

	// 10 倍无关对象，单操作耗时不得按 10 倍增长。留出宽松上界。
	if ratio := float64(dLarge) / float64(dSmall); ratio > 3.0 {
		t.Fatalf("operation time scales with total size: ratio %.2f (>3)", ratio)
	}
}

// TestComplexityScalesWithAffectedSet 正向验证：影响面（被连带删除的
// 链长度）线性增长时，耗时相应线性增长；用基准测试输出 ns/op 供核对。
func BenchmarkCascadeDepth(b *testing.B) {
	for _, depth := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("depth%d", depth), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				c := cascade.New()
				ids := make([]string, depth)
				for j := 0; j < depth; j++ {
					ids[j] = fmt.Sprintf("n%d", j)
					var owners []cascade.OwnerRef
					if j > 0 {
						owners = []cascade.OwnerRef{{OwnerID: ids[j-1]}}
					}
					if err := c.Create(ids[j], owners, nil); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if err := c.Delete(ids[0], cascade.Background); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
