package ontology

import (
	"fmt"
	"testing"
)

// TestLookupAllRedeclarationCombinations 在 T0→T1→T2→T3 链条上，
// 枚举 {T1,T2,T3} 是否重新声明的全部 8 种组合，
// 验证 T3 上查找结果命中离 T3 最近的显式声明。
func TestLookupAllRedeclarationCombinations(t *testing.T) {
	chain := []string{"T0", "T1", "T2", "T3"}
	for mask := 0; mask < 8; mask++ {
		name := fmt.Sprintf("mask-%03b", mask)
		t.Run(name, func(t *testing.T) {
			r := NewRegistry()
			mustCreateChain(t, r, chain...)
			if err := r.DeclareRule("T0", "p", NewRule([]string{"a", "b", "c", "d"})); err != nil {
				t.Fatal(err)
			}
			// 每个重新声明层级使用逐级收窄的集合。
			narrow := map[string][]string{
				"T1": {"a", "b", "c"},
				"T2": {"a", "b"},
				"T3": {"a"},
			}
			wantSource := "T0"
			for i, id := range chain[1:] {
				if mask&(1<<i) != 0 {
					if err := r.DeclareRule(id, "p", NewRule(narrow[id])); err != nil {
						t.Fatal(err)
					}
					wantSource = id
				}
			}
			res, err := r.EffectiveRule("T3", "p")
			if err != nil {
				t.Fatal(err)
			}
			if res.SourceType != wantSource {
				t.Fatalf("source: want %q, got %q", wantSource, res.SourceType)
			}
			wantRule := NewRule([]string{"a", "b", "c", "d"})
			if wantSource != "T0" {
				wantRule = NewRule(narrow[wantSource])
			}
			if !res.Rule.Equal(wantRule) {
				t.Fatalf("rule: want %v, got %v", wantRule.Values(), res.Rule.Values())
			}
			// 遍历路径必须从 T3 开始、在命中类型处结束。
			if res.Path[0] != "T3" || res.Path[len(res.Path)-1] != wantSource {
				t.Fatalf("path endpoints wrong: %v", res.Path)
			}
		})
	}
}

// TestParentRedeclarationPropagation 父层级新的重新声明影响未自行声明的子孙，
// 但不影响已自行声明的子类型。
func TestParentRedeclarationPropagation(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "T0", "T1", "T2", "T3")
	declare := func(id string, vals ...string) {
		t.Helper()
		if err := r.DeclareRule(id, "p", NewRule(vals)); err != nil {
			t.Fatal(err)
		}
	}
	effectiveAt := func(id string) Rule {
		t.Helper()
		res, err := r.EffectiveRule(id, "p")
		if err != nil {
			t.Fatal(err)
		}
		return res.Rule
	}

	declare("T0", "a", "b", "c", "d")
	declare("T1", "a", "b")
	if got := effectiveAt("T2"); !got.Equal(NewRule([]string{"a", "b"})) {
		t.Fatalf("T2 should follow T1, got %v", got.Values())
	}
	// T1 再次收窄，未自行声明的 T2 跟随变化。
	declare("T1", "a", "b") // 相等允许
	declare("T1", "a")
	if got := effectiveAt("T2"); !got.Equal(NewRule([]string{"a"})) {
		t.Fatalf("T2 should track T1 redeclaration, got %v", got.Values())
	}
	// T2 自行声明后，T1 的进一步变化不再影响 T2 及其子孙。
	declare("T2", "a")
	declare("T1", "a") // 只能收窄到相等或子集
	res, err := r.EffectiveRule("T2", "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceType != "T2" {
		t.Fatalf("T2 has own declaration, want source T2, got %q", res.SourceType)
	}
	if res3, _ := r.EffectiveRule("T3", "p"); res3.SourceType != "T2" {
		t.Fatalf("T3 should inherit T2 declaration, got %q", res3.SourceType)
	}
}

// TestLookupDeterministic 无类型变更时，重复查找结果必须一致。
func TestLookupDeterministic(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "T0", "T1", "T2")
	if err := r.DeclareRule("T0", "p", NewRule([]string{"a", "b"})); err != nil {
		t.Fatal(err)
	}
	first, err := r.EffectiveRule("T2", "p")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := r.EffectiveRule("T2", "p")
		if err != nil {
			t.Fatal(err)
		}
		if got.SourceType != first.SourceType || !got.Rule.Equal(first.Rule) {
			t.Fatalf("lookup not deterministic: %+v vs %+v", first, got)
		}
	}
}

// TestLookupDepthBound 查找遍历长度不超过具体类型到声明祖先的实际深度，
// 且与继承体系中无关分支的数量无关。
func TestLookupDepthBound(t *testing.T) {
	const depth = 40
	r := NewRegistry()
	ids := make([]string, depth+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("chain-%d", i)
	}
	mustCreateChain(t, r, ids...)
	if err := r.DeclareRule(ids[0], "p", NewRule([]string{"a"})); err != nil {
		t.Fatal(err)
	}

	addBranches := func(n, offset int) {
		t.Helper()
		for i := 0; i < n; i++ {
			branch := fmt.Sprintf("branch-%d-%d", offset, i)
			if err := r.CreateType(branch, "", false); err != nil {
				t.Fatal(err)
			}
			for j := 0; j < 5; j++ {
				if err := r.CreateType(fmt.Sprintf("%s-%d", branch, j), branch, false); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	addBranches(200, 0)
	deepest := ids[depth]
	before, err := r.EffectiveRule(deepest, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Path) != depth+1 {
		t.Fatalf("traversal length must equal actual depth %d, got %d", depth+1, len(before.Path))
	}

	// 无关分支数量增长 10 倍后，遍历长度不变。
	addBranches(2000, 1)
	after, err := r.EffectiveRule(deepest, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Path) != len(before.Path) {
		t.Fatalf("traversal length grew with unrelated branches: %d -> %d",
			len(before.Path), len(after.Path))
	}

	// 审计记录中的路径同样受深度约束。
	log := r.AuditLog()
	last := log[len(log)-1]
	if len(last.Path) != depth+1 || last.SourceType != ids[0] {
		t.Fatalf("audit record mismatch: path=%d source=%q", len(last.Path), last.SourceType)
	}
}
