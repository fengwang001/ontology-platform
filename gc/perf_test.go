package gc

import (
	"fmt"
	"testing"
)

// 性能验证思路：用 LastOpSteps 统计一次操作实际触及的对象/引用次数，
// 证明它只取决于被影响的对象与引用数量，而不随无关对象总数增长。
// 这比墙钟时间更稳定、可复现（墙钟基准见 BenchmarkDeleteCascade）。

// buildCluster 构造 noise 个互不相干的对象，外加一条 length 长的
// 属主链 chain-0 <- chain-1 <- ... <- chain-(length-1)。
func buildCluster(tb testing.TB, noise, length int) *Controller {
	tb.Helper()
	c := New()
	for i := 0; i < noise; i++ {
		if err := c.Create(fmt.Sprintf("noise-%d", i), nil, nil); err != nil {
			tb.Fatal(err)
		}
	}
	if err := c.Create("chain-0", nil, nil); err != nil {
		tb.Fatal(err)
	}
	for i := 1; i < length; i++ {
		if err := c.Create(fmt.Sprintf("chain-%d", i),
			[]OwnerRef{{OwnerID: fmt.Sprintf("chain-%d", i-1), Block: true}}, nil); err != nil {
			tb.Fatal(err)
		}
	}
	return c
}

func deleteRootSteps(tb testing.TB, noise, length int) int {
	tb.Helper()
	c := buildCluster(tb, noise, length)
	if err := c.Delete("chain-0", Background, ts(1)); err != nil {
		tb.Fatal(err)
	}
	return c.LastOpSteps()
}

// 同一级联，无关对象总数放大 50 倍，触及步数必须完全相等。
func TestStepsIndependentOfUnrelatedObjects(t *testing.T) {
	small := deleteRootSteps(t, 1_000, 50)
	large := deleteRootSteps(t, 50_000, 50)
	t.Logf("cascade over 50-object chain: steps(noise=1k)=%d steps(noise=50k)=%d", small, large)
	if small != large {
		t.Fatalf("steps must not depend on unrelated objects: %d != %d", small, large)
	}
	if small == 0 {
		t.Fatalf("steps counter not recording")
	}
}

// 步数随实际影响的链长线性增长（验证计数器确实在度量影响面）。
func TestStepsGrowWithAffectedObjects(t *testing.T) {
	s50 := deleteRootSteps(t, 0, 50)
	s100 := deleteRootSteps(t, 0, 100)
	t.Logf("steps(chain=50)=%d steps(chain=100)=%d", s50, s100)
	if s100 <= s50 {
		t.Fatalf("steps should grow with affected objects: %d vs %d", s50, s100)
	}
	// 粗略线性：2 倍链长的步数不应超过 4 倍。
	if s100 > 4*s50 {
		t.Fatalf("steps should grow linearly: %d vs %d", s50, s100)
	}
}

// 环检测只访问祖先链，与无关对象总数无关。
func TestCycleCheckStepsIndependent(t *testing.T) {
	steps := func(noise int) int {
		c := buildCluster(t, noise, 30)
		// 让链尾引用链首：沿 30 个祖先发现环。
		err := c.SetOwners("chain-0", []OwnerRef{{OwnerID: "chain-29"}}, ts(1))
		if err == nil || err.Kind != KindCycle {
			t.Fatalf("expected cycle error, got %v", err)
		}
		return c.LastOpSteps()
	}
	small, large := steps(1_000), steps(50_000)
	t.Logf("cycle check over 30-deep chain: steps(noise=1k)=%d steps(noise=50k)=%d", small, large)
	if small != large {
		t.Fatalf("cycle check steps must not depend on unrelated objects: %d != %d", small, large)
	}
}

func BenchmarkDeleteCascade(b *testing.B) {
	for _, noise := range []int{0, 100_000} {
		b.Run(fmt.Sprintf("noise=%d", noise), func(b *testing.B) {
			c := buildCluster(b, noise, 100)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := c.Delete("chain-0", Background, ts(int64(i))); err != nil {
					b.Fatal(err)
				}
				// 重建链以供下一次迭代（不计时）。
				b.StopTimer()
				c = buildCluster(b, noise, 100)
				b.StartTimer()
			}
		})
	}
}
