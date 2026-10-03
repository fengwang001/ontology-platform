package saga_test

import (
	"fmt"
	"testing"

	"ontology/effect"
	"ontology/journal"
	"ontology/saga"
)

// TestExhaustive 枚举 n≤4、B∈{0,1} 的全部 p，生成深度至 8 的所有合法动作序列，
// 对每个日志前缀对照 Recover 与朴素模拟，并从恢复计划走到终态检查恒等式。
func TestExhaustive(t *testing.T) {
	for n := 1; n <= 4; n++ {
		for p := 0; p < n; p++ {
			for _, budget := range []int{0, 1} {
				t.Run(fmt.Sprintf("n=%d/p=%d/B=%d", n, p, budget), func(t *testing.T) {
					nodes := 0
					var walk func(path []act, recs []journal.Record, depth int)
					walk = func(path []act, recs []journal.Record, depth int) {
						nodes++
						checkCrash(t, n, p, budget, path, recs)
						if depth == 8 {
							return
						}
						for _, a := range nextActs(recs, n, p, budget) {
							walk(append(append([]act{}, path...), a),
								append(append([]journal.Record{}, recs...), a.record()), depth+1)
						}
					}
					walk(nil, []journal.Record{{Kind: journal.KindBegin}}, 0)
					t.Logf("输入: n=%d p=%d B=%d 深度≤8；输出: %d 个崩溃点全部与朴素模拟一致并收敛", n, p, budget, nodes)
				})
			}
		}
	}
}

// checkCrash 在崩溃点重放前缀、对照 Recover、走到终态并检查恒等式。
func checkCrash(t *testing.T, n, p, budget int, path []act, recs []journal.Record) {
	t.Helper()
	jr, eff := journal.New(), effect.New()
	s := saga.New(jr, eff)
	id := []byte("inst")
	if err := s.Begin(id, n, p, budget); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for i, a := range path {
		if err := applyAct(s, id, a); err != nil {
			t.Fatalf("路径 %v 第 %d 步 %v 被拒: %v", path, i, a, err)
		}
	}
	if got := jr.Read("inst"); fmt.Sprint(got) != fmt.Sprint(recs) {
		t.Fatalf("重放日志不符: 得到 %v，期望 %v", got, recs)
	}
	plan, err := s.Recover(id)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if want := naivePlan(recs, n, p, budget); plan != want {
		t.Fatalf("日志 %v: Recover 得 %v，朴素模拟得 %v", recs, plan, want)
	}
	if again, _ := s.Recover(id); again != plan || jr.Len("inst") != len(recs) {
		t.Fatalf("Recover 不幂等: %v → %v", plan, again)
	}
	do := func(err error) {
		if err != nil {
			t.Fatalf("日志 %v 后推进失败: %v", recs, err)
		}
	}
	for steps := 0; ; steps++ {
		if steps > 4*n+8 {
			t.Fatalf("日志 %v 后未收敛", recs)
		}
		switch pl, _ := s.Recover(id); pl.Kind {
		case saga.Forward:
			do(s.Intent(id, pl.Step))
			do(s.Done(id, pl.Step))
		case saga.Compensate:
			do(s.CompIntent(id, pl.Step))
			do(s.CompDone(id, pl.Step))
		case saga.ProbePivot:
			do(s.Probe(id, true))
		default: // 已完成 / 已补偿 / 转人工
			checkInvariants(t, jr, eff, "inst", n, p)
			return
		}
	}
}

// checkInvariants：fwd/comp 新增各至多 1、CD(j) 前必有 I(j)、j≥p 不被补偿、计数不超日志条数。
func checkInvariants(t *testing.T, jr *journal.Journal, eff *effect.Table, id string, n, p int) {
	t.Helper()
	recs := jr.Read(id)
	for i := 0; i < n; i++ {
		for _, ph := range []effect.Phase{effect.Fwd, effect.Comp} {
			if a, _ := eff.Stats(id, i, ph); a > 1 {
				t.Errorf("步骤 %d 阶段 %d 新增 %d 次", i, ph, a)
			}
		}
	}
	seen := make(map[int]bool)
	for _, r := range recs {
		switch r.Kind {
		case journal.KindIntent:
			seen[r.Step] = true
		case journal.KindCompIntent, journal.KindCompDone:
			if r.Step >= p {
				t.Errorf("步骤 %d ≥ p=%d 被补偿: %v", r.Step, p, recs)
			}
			if r.Kind == journal.KindCompDone && !seen[r.Step] {
				t.Errorf("CD(%d) 之前无 I(%d): %v", r.Step, r.Step, recs)
			}
		}
	}
	if a, d := eff.Totals(); a+d > len(recs) {
		t.Errorf("副作用计数 %d+%d 超过日志条数 %d", a, d, len(recs))
	}
}
