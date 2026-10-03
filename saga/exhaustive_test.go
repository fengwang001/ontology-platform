package saga_test

import (
	"fmt"
	"testing"

	"ontology/effect"
	"ontology/journal"
	"ontology/saga"
)

// TestExhaustive 对 n≤4、B∈{0,1} 的全部 p，枚举深度≤8 的合法动作
// 序列；每个前缀（崩溃点）用 Recover 对照朴素模拟，再从恢复计划
// 走到终态检查恒等式。probeCommit 两档各跑一遍以覆盖探测两分支。
func TestExhaustive(t *testing.T) {
	for _, probeCommit := range []bool{true, false} {
		for n := 1; n <= 4; n++ {
			for p := 0; p < n; p++ {
				for b := 0; b <= 1; b++ {
					name := fmt.Sprintf("n=%d p=%d B=%d probe=%v", n, p, b, probeCommit)
					t.Run(name, func(t *testing.T) {
						cfg := config{t: t, n: n, p: p, b: b, probe: probeCommit}
						t.Logf("输入: %s；判定依据: 日志最后一条记录 + F(i,瞬时) 计数", name)
						cfg.dfs(nil, -1, false, 0)
						t.Logf("输出: 全部前缀 Recover 与朴素模拟一致，终态恒等式成立")
					})
				}
			}
		}
	}
}

type config struct {
	t       *testing.T
	n, p, b int
	probe   bool
}

// dfs 枚举动作序列；每个节点都是一个崩溃点前缀。
func (c *config) dfs(path []action, inflight int, comp bool, depth int) {
	c.visit(path, inflight, comp)
	if depth == 8 {
		return
	}
	pl := c.replayPlan(path)
	for _, a := range legalActions(pl, inflight, comp) {
		in2, comp2 := inflight, comp
		applyShadow(a, &in2, &comp2)
		c.dfs(append(path[:len(path):len(path)], a), in2, comp2, depth+1)
	}
}

// replayPlan 重放路径并用朴素模拟得出计划。
func (c *config) replayPlan(path []action) saga.Plan {
	s := newSim(c.n, c.p, c.b)
	s.step(journal.Record{Kind: journal.KindBegin})
	for _, a := range path {
		s.step(a.record(c.p))
	}
	return s.plan
}

// visit 在一个前缀上：对照 Recover 与朴素模拟，再走到终态。
func (c *config) visit(path []action, inflight int, comp bool) {
	t := c.t
	jr, ef := journal.New(), effect.New()
	m := saga.New(jr, ef)
	must(t, m.Begin("x", c.n, c.p, c.b))
	for _, a := range path {
		if err := a.do(m, "x"); err != nil {
			t.Fatalf("重放 %v 失败: %v", path, err)
		}
	}
	want := c.replayPlan(path)
	before := jr.Len("x")
	got, err := m.Recover("x")
	must(t, err)
	got2, err := m.Recover("x")
	must(t, err)
	if got != want || got2 != want {
		t.Fatalf("前缀 %v: Recover=%v 再恢复=%v 朴素模拟=%v（依据：最后一条记录+瞬时计数）",
			path, got, got2, want)
	}
	if jr.Len("x") != before {
		t.Fatalf("前缀 %v: Recover 追加了记录", path)
	}
	term := c.driveToTerminal(m)
	c.checkInvariants(jr.Records("x"), ef, term)
}

// driveToTerminal 从当前状态按 Recover 计划走到终态。
func (c *config) driveToTerminal(m *saga.Manager) saga.Plan {
	for i := 0; i < 200; i++ {
		pl, err := m.Recover("x")
		must(c.t, err)
		switch pl.Kind {
		case saga.PlanForward:
			must(c.t, m.Intent("x", pl.Step))
			must(c.t, m.Done("x", pl.Step))
		case saga.PlanCompensate:
			must(c.t, m.CompIntent("x", pl.Step))
			must(c.t, m.CompDone("x", pl.Step))
		case saga.PlanProbe:
			must(c.t, m.Probe("x", c.probe))
		default:
			return pl
		}
	}
	c.t.Fatalf("n=%d p=%d B=%d: 200 步未收敛", c.n, c.p, c.b)
	return saga.Plan{}
}

// checkInvariants 检查终态恒等式。
func (c *config) checkInvariants(log []journal.Record, ef *effect.Table, term saga.Plan) {
	t := c.t
	switch term.Kind {
	case saga.PlanFinished, saga.PlanCompensated, saga.PlanManual:
	default:
		t.Fatalf("终态非法: %v", term)
	}
	intended := make(map[int]bool)
	for _, r := range log {
		switch r.Kind {
		case journal.KindIntent:
			intended[r.Step] = true
		case journal.KindCompIntent, journal.KindCompDone:
			if r.Step >= c.p {
				t.Fatalf("步骤 %d ≥ 枢轴 %d 被补偿: %v", r.Step, c.p, log)
			}
			if r.Kind == journal.KindCompDone && !intended[r.Step] {
				t.Fatalf("CD(%d) 之前无 I(%d): %v", r.Step, r.Step, log)
			}
		}
	}
	for i := 0; i < c.n; i++ {
		fa, fr := ef.Stats(effect.Key{ID: "x", Step: i, Phase: effect.Fwd})
		ca, cr := ef.Stats(effect.Key{ID: "x", Step: i, Phase: effect.Comp})
		if fa > 1 || ca > 1 {
			t.Fatalf("步骤 %d 新增超过 1 次: fwd=%d comp=%d", i, fa, ca)
		}
		if fa+fr+ca+cr > len(log) {
			t.Fatalf("步骤 %d 计数超过日志条数", i)
		}
	}
}
