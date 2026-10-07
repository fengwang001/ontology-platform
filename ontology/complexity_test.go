package ontology

import (
	"fmt"
	"math"
	"testing"
)

// 单条链接存在性判定的开销不随链接类型累计的创建/撤销历史总量线性增长。
// 验证方式：在链接类型上制造 N 次其他链接的创建/撤销后，
// 探针的比较步数保持有界（只随目标链接自身的区间数对数增长）。
func TestLinkProbeStepsIndependentOfTypeHistory(t *testing.T) {
	must := mkMust(t)
	s := NewStore()
	must(s.DefineObjectType("T", nil))
	must(s.DefineLinkType("L", "T", "T", ManyToMany))
	must(s.PutObject("T", "probe-src", nil))
	must(s.PutObject("T", "probe-dst", nil))

	// 目标链接只创建一次（区间数 = 1）。
	vProbe := must(s.AddLink("L", "probe-src", "probe-dst"))

	measure := func() int {
		exists, steps, err := s.ProbeLink("L", "probe-src", "probe-dst", vProbe)
		if err != nil || !exists {
			t.Fatalf("probe failed: exists=%v err=%v", exists, err)
		}
		return steps
	}

	before := measure()

	// 制造类型级别的海量创建/撤销历史（其他链接）。
	const churn = 20000
	must(s.PutObject("T", "churn-src", nil))
	for i := 0; i < churn; i++ {
		dst := fmt.Sprintf("churn-dst-%d", i%100)
		if i < 100 {
			must(s.PutObject("T", dst, nil))
		}
		if _, err := s.AddLink("L", "churn-src", dst); err == nil {
			must(s.RemoveLink("L", "churn-src", dst))
		}
	}

	after := measure()
	if after != before {
		t.Fatalf("probe steps grew with type-level history: before=%d after=%d", before, after)
	}
	if after > 2 {
		t.Fatalf("probe steps %d exceed bound for single-interval link", after)
	}
}

// 目标链接自身被反复创建/撤销 k 次时，判定开销为 O(log k)。
func TestLinkProbeStepsLogarithmicInOwnHistory(t *testing.T) {
	must := mkMust(t)
	s := NewStore()
	must(s.DefineObjectType("T", nil))
	must(s.DefineLinkType("L", "T", "T", ManyToMany))
	must(s.PutObject("T", "x", nil))
	must(s.PutObject("T", "y", nil))

	const k = 1024
	var lastV Version
	for i := 0; i < k; i++ {
		lastV = must(s.AddLink("L", "x", "y"))
		must(s.RemoveLink("L", "x", "y"))
	}
	_, steps, err := s.ProbeLink("L", "x", "y", lastV)
	if err != nil {
		t.Fatal(err)
	}
	bound := int(math.Ceil(math.Log2(k))) + 1
	if steps > bound {
		t.Fatalf("steps=%d exceed log2(%d)+1=%d", steps, k, bound)
	}
}
