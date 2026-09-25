package pipe

import (
	"testing"

	"ontology/pred"
)

// 规划是单遍扫描：m 个全无状态过滤器合并为一个 AND，
// 相邻可交换性检查次数随 m 线性增长（= m-1），而非二次。
func TestPlanChecksLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		ops := make([]Op, m)
		for i := range ops {
			i := i
			ops[i] = Op{Name: "f", Pred: pred.Pred{Name: "f", Field: "Val",
				F: func(e pred.Event) bool { return e.Val >= int64(i%5) }}}
		}
		var p Planner
		steps, err := p.Plan(ops)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if len(steps) != 1 {
			t.Fatalf("m=%d: 应合并为 1 步, 得 %d", m, len(steps))
		}
		if p.checks >= m {
			t.Fatalf("m=%d: 检查次数 %d 非线性（上界 m-1=%d）", m, p.checks, m-1)
		}
	}
}

// 检查次数随规模严格线性：m 扩大 100 倍，checks 扩大不超过 100 倍多一点。
func TestPlanChecksNotQuadratic(t *testing.T) {
	count := func(m int) int {
		ops := make([]Op, m)
		for i := range ops {
			ops[i] = Op{Name: "f", Pred: pred.Even()}
		}
		var p Planner
		if _, err := p.Plan(ops); err != nil {
			t.Fatal(err)
		}
		return p.checks
	}
	small, big := count(100), count(10000)
	if big > 101*small {
		t.Fatalf("检查次数疑似二次增长: m=100→%d, m=10000→%d", small, big)
	}
}
