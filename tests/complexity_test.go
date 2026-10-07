package ontology_test

import (
	"fmt"
	"testing"

	"ontology/ontology"
)

func buildNoisyRegistry(n int) *ontology.HookRegistry {
	reg := ontology.NewHookRegistry()
	noop := ontology.Hook{ID: "h", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { return nil }}
	for j := 0; j < n; j++ {
		from := fmt.Sprintf("x%d", j)
		to := fmt.Sprintf("y%d", j)
		reg.RegisterTransition(from, to, noop)
		reg.RegisterEntry(to, noop)
	}
	reg.RegisterTransition("s0", "s1", noop)
	return reg
}

func BenchmarkResolve100Hooks(b *testing.B)    { benchmarkResolve(b, 50) }
func BenchmarkResolve10000Hooks(b *testing.B)  { benchmarkResolve(b, 5000) }
func BenchmarkResolve100000Hooks(b *testing.B) { benchmarkResolve(b, 50000) }

func benchmarkResolve(b *testing.B, edges int) {
	reg := buildNoisyRegistry(edges)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.Resolve("s0", "s1")
	}
}

// TestResolveComplexityProof 以可验证方式证明触发范围解析开销
// 不随「全部转移关系总数 / 全部已注册钩子总数」增长：
// ResolveProbed 回报的 CellsTouched = 2（两次哈希键查找）+ 实际命中数。
// 注册表从 4 个钩子扩张到 100000 个钩子，只要命中数不变，触碰单元数恒定。
func TestResolveComplexityProof(t *testing.T) {
	reg := ontology.NewHookRegistry()
	noop := func(id string) ontology.Hook {
		return ontology.Hook{ID: id, Semantic: ontology.CommitImmediately,
			Check: func(*ontology.TransitionContext) error { return nil }}
	}

	// 被探测的固定转移：s0->s1，固定 2 个具体钩子 + 1 个进入钩子。
	reg.RegisterTransition("s0", "s1", noop("edge-0"))
	reg.RegisterTransition("s0", "s1", noop("edge-1"))
	reg.RegisterEntry("s1", noop("entry-1"))

	measure := func() ontology.Probe {
		hooks, probe := reg.ResolveProbed("s0", "s1")
		if len(hooks) != 3 {
			t.Fatalf("命中数异常: %d", len(hooks))
		}
		return probe
	}

	base := measure()
	const sizes = 5
	var growth [sizes]int
	var touched [sizes]int
	for i := 0; i < sizes; i++ {
		// 每轮向注册表灌入 20000 个与 s0->s1 无关的钩子（10000 条不同转移边）。
		for j := 0; j < 10000; j++ {
			from := fmt.Sprintf("x%d-%d", i, j)
			to := fmt.Sprintf("y%d-%d", i, j)
			reg.RegisterTransition(from, to, noop(fmt.Sprintf("noise-edge-%d-%d", i, j)))
			reg.RegisterEntry(to, noop(fmt.Sprintf("noise-entry-%d-%d", i, j)))
		}
		p := measure()
		growth[i] = reg.TransitionHookCount() + reg.EntryHookCount()
		touched[i] = p.CellsTouched
		fmt.Printf("注册表规模=%6d 个钩子（含全部具体转移关系 %d 条）-> 解析 s0->s1 触碰单元数=%d\n",
			growth[i], reg.TransitionHookCount(), p.CellsTouched)
		if p.CellsTouched != base.CellsTouched {
			t.Fatalf("触碰单元数随注册表规模增长: base=%d now=%d", base.CellsTouched, p.CellsTouched)
		}
	}
	fmt.Printf("判定依据: CellsTouched 恒为 %d = 2 次哈希键查找 + 3 个实际命中钩子，与注册表规模无关\n",
		base.CellsTouched)
	if base.CellsTouched != 5 {
		t.Fatalf("期望 2+3=5，实际 %d", base.CellsTouched)
	}

	// 自转移解析同构：只命中显式注册的 (s9,s9)，不随规模变化。
	reg.RegisterTransition("s9", "s9", noop("self-9"))
	_, selfProbe := reg.ResolveProbed("s9", "s9")
	if selfProbe.CellsTouched != 3 { // 2 次查表 + 1 命中（s9 无进入钩子）
		t.Fatalf("自转移解析触碰单元数异常: %d", selfProbe.CellsTouched)
	}
	fmt.Printf("自转移 s9->s9（无进入钩子）触碰单元数=%d，同样与规模无关\n", selfProbe.CellsTouched)
}
