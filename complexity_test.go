package ontology

import (
	"fmt"
	"testing"
)

// TestExclusionComplexityBounded 可验证地证明：
// 判定一个浅层诊断是否除外的探测次数为 1+树高，
// 在目录编码总数与档案中无关编码数量成倍增长时保持不变。
func TestExclusionComplexityBounded(t *testing.T) {
	measure := func(totalCodes, archiveSize int) int {
		e := NewEngine()
		cat := e.cat
		// 目标诊断是浅层链 root -> leaf（高度 1）。
		mustAddCode(t, e, "root", "", false)
		mustAddCode(t, e, "leaf", "root", false)
		// 与目标无关的编码形成多条独立长链，把目录撑大。
		for i := 0; i < totalCodes; i++ {
			mustAddCode(t, e, fmt.Sprintf("x%d", i), "", false)
		}
		arch := newArchive()
		// 档案里放入大量与 leaf 无祖先关系的编码。
		for i := 0; i < archiveSize; i++ {
			arch.add(fmt.Sprintf("x%d", i))
		}
		excluded, probes := arch.excludedProbes("leaf", cat)
		if excluded {
			t.Fatal("leaf 不应被除外")
		}
		return probes
	}

	p1 := measure(100, 50)
	p2 := measure(10000, 5000)
	p3 := measure(100000, 50000)
	// 浅层诊断自身 + 1 个祖先，探测恒为 2 次，与规模无关。
	if !(p1 == 2 && p2 == 2 && p3 == 2) {
		t.Fatalf("探测次数应恒为 2: %d %d %d", p1, p2, p3)
	}
	t.Logf("除外判定探测次数（目录/档案无关项 100/50, 1万/5千, 10万/5万）: %d %d %d", p1, p2, p3)
}

// TestExclusionProbesEqualHeight 证明探测次数恰为 1 + 树高。
func TestExclusionProbesEqualHeight(t *testing.T) {
	e := NewEngine()
	const depth = 50
	mustAddCode(t, e, "n0", "", false)
	for i := 1; i <= depth; i++ {
		mustAddCode(t, e, fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i-1), false)
	}
	arch := newArchive()
	_, probes := arch.excludedProbes(fmt.Sprintf("n%d", depth), e.cat)
	if probes != depth+1 {
		t.Fatalf("探测次数应为 1+树高=%d, got %d", depth+1, probes)
	}
	// 命中间档祖先后提前停止。
	arch.add("n10")
	excluded, probes := arch.excludedProbes(fmt.Sprintf("n%d", depth), e.cat)
	if !excluded || probes != depth-10+1 {
		t.Fatalf("命中 n10 后应除外且探测 %d 次, got excluded=%v probes=%d",
			depth-10+1, excluded, probes)
	}
}

func BenchmarkExcluded(b *testing.B) {
	e := NewEngine()
	mustAddCodeB(b, e, "root", "", false)
	mustAddCodeB(b, e, "leaf", "root", false)
	arch := newArchive()
	for i := 0; i < 100000; i++ {
		code := fmt.Sprintf("x%d", i)
		if err := e.AddCode(code, "", false); err != nil {
			b.Fatal(err)
		}
		arch.add(code)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if arch.excluded("leaf", e.cat) {
			b.Fatal("不应除外")
		}
	}
}

func mustAddCodeB(b *testing.B, e *Engine, code, parent string, accidental bool) {
	b.Helper()
	if err := e.AddCode(code, parent, accidental); err != nil {
		b.Fatal(err)
	}
}
