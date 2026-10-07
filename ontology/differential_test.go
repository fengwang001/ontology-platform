package ontology_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/ontology"
	"ontology/ontology/naive"
)

// hookSpec 描述一个确定性钩子，用于同时构建优化实现与朴素模型。
type hookSpec struct {
	name   string
	kind   int // 0=transition 1=enter
	from   string
	to     string
	commit int // 0=rollback 1=commit
	fail   bool
	emit   bool
}

func (s hookSpec) ontologyHook() *ontology.Hook {
	spec := s
	h := &ontology.Hook{
		Name:   spec.name,
		From:   ontology.Stage(spec.from),
		To:     ontology.Stage(spec.to),
		Commit: ontology.CommitSemantics(spec.commit),
		Fn: func(ctx *ontology.HookContext) error {
			if spec.emit {
				ctx.Emit("log:" + spec.name)
			}
			if spec.fail {
				return fmt.Errorf("deterministic failure of %s", spec.name)
			}
			return nil
		},
	}
	return h
}

func (s hookSpec) naiveHook() *naive.Hook {
	spec := s
	return &naive.Hook{
		Name:   spec.name,
		Kind:   naive.HookKind(spec.kind),
		From:   spec.from,
		To:     spec.to,
		Commit: naive.CommitSemantics(spec.commit),
		Fn: func(ctx *naive.Context) error {
			if spec.emit {
				ctx.Emit("log:" + spec.name)
			}
			if spec.fail {
				return fmt.Errorf("deterministic failure of %s", spec.name)
			}
			return nil
		},
	}
}

// scenario 是一次差分对照的完整输入。
type scenario struct {
	stages    []string
	edges     []naive.Edge
	terminals []string
	hooks     []hookSpec
	initial   string
	requests  []string
}

func genScenario(rng *rand.Rand) scenario {
	nStages := 2 + rng.Intn(5)
	stages := make([]string, nStages)
	for i := range stages {
		stages[i] = fmt.Sprintf("s%d", i)
	}
	var edges []naive.Edge
	for _, f := range stages {
		for _, to := range stages { // 含自环，允许环
			if rng.Intn(2) == 0 {
				edges = append(edges, naive.Edge{From: f, To: to})
			}
		}
	}
	if len(edges) == 0 {
		edges = append(edges, naive.Edge{From: stages[0], To: stages[0]})
	}
	var terminals []string
	for _, s := range stages {
		if rng.Intn(4) == 0 {
			terminals = append(terminals, s)
		}
	}
	nHooks := rng.Intn(9)
	hooks := make([]hookSpec, nHooks)
	for i := range hooks {
		hooks[i] = hookSpec{
			name:   fmt.Sprintf("h%d", i),
			kind:   rng.Intn(2),
			from:   stages[rng.Intn(nStages)],
			to:     stages[rng.Intn(nStages)],
			commit: rng.Intn(2),
			fail:   rng.Intn(3) == 0,
			emit:   rng.Intn(2) == 0,
		}
	}
	nReq := 50 + rng.Intn(150)
	requests := make([]string, nReq)
	for i := range requests {
		if rng.Intn(20) == 0 {
			requests[i] = "ghost" // 未声明的目标阶段
		} else {
			requests[i] = stages[rng.Intn(nStages)]
		}
	}
	return scenario{
		stages:    stages,
		edges:     edges,
		terminals: terminals,
		hooks:     hooks,
		initial:   stages[rng.Intn(nStages)],
		requests:  requests,
	}
}

// TestDifferentialAgainstNaive 在大量随机转移请求序列下，
// 将优化实现（哈希索引解析）与朴素模型（全量遍历）逐条对照。
func TestDifferentialAgainstNaive(t *testing.T) {
	const seeds = 300
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sc := genScenario(rng)

		ot, err := ontology.NewObjectType(ontology.ObjectTypeDef{
			Name:      "diff",
			Stages:    toStages(sc.stages),
			Edges:     toOntologyEdges(sc.edges),
			Terminals: toStages(sc.terminals),
		})
		if err != nil {
			t.Fatalf("seed=%d NewObjectType: %v", seed, err)
		}
		var nhooks []*naive.Hook
		for _, spec := range sc.hooks {
			oh := spec.ontologyHook()
			if spec.kind == 0 {
				ot.Hooks().RegisterTransition(oh)
			} else {
				ot.Hooks().RegisterEnter(oh)
			}
			nhooks = append(nhooks, spec.naiveHook())
		}
		inst, err := ot.NewInstance("diff-obj", ontology.Stage(sc.initial))
		if err != nil {
			t.Fatalf("seed=%d NewInstance: %v", seed, err)
		}
		nm := naive.New(sc.stages, sc.edges, sc.terminals, nhooks, sc.initial)

		for i, to := range sc.requests {
			errFast := ot.Transition(inst, ontology.Stage(to))
			errNaive := nm.Transition(to)
			kindFast, okFast := ontology.KindOf(errFast)
			if (errFast == nil) != (errNaive == nil) {
				t.Fatalf("seed=%d req=%d to=%s: 结果不一致 fast=%v naive=%v\n输入: %+v",
					seed, i, to, errFast, errNaive, sc)
			}
			if errFast != nil && (!okFast || int(kindFast) != naiveKindOf(errNaive)) {
				t.Fatalf("seed=%d req=%d to=%s: 错误类别不一致 fast=%v naive=%v\n输入: %+v",
					seed, i, to, errFast, errNaive, sc)
			}
			if string(inst.Stage()) != nm.Stage() {
				t.Fatalf("seed=%d req=%d to=%s: 阶段不一致 fast=%v naive=%v\n输入: %+v",
					seed, i, to, inst.Stage(), nm.Stage(), sc)
			}
		}

		// 对照完整轨迹：阶段历史、每条记录的钩子触发序列、记录副作用。
		fastTrace := inst.Trace()
		naiveTrace := nm.Trace()
		if len(fastTrace) != len(naiveTrace) {
			t.Fatalf("seed=%d: trace 长度不一致 %d vs %d", seed, len(fastTrace), len(naiveTrace))
		}
		for i := range fastTrace {
			if !traceRecEqual(fastTrace[i], naiveTrace[i]) {
				t.Fatalf("seed=%d trace[%d] 不一致:\nfast=%+v\nnaive=%+v\n输入: %+v",
					seed, i, fastTrace[i], naiveTrace[i], sc)
			}
		}
		if !stagesEqual(inst.History(), nm.History()) {
			t.Fatalf("seed=%d: history 不一致 fast=%v naive=%v", seed, inst.History(), nm.History())
		}
		if !sideEffectsEqual(inst.SideEffects(), nm.SideEffects()) {
			t.Fatalf("seed=%d: sideEffects 不一致 fast=%+v naive=%+v", seed, inst.SideEffects(), nm.SideEffects())
		}

		t.Logf("seed=%d 输入: stages=%v edges=%d terminals=%v hooks=%d requests=%d; "+
			"输出: history=%v 双方逐条一致; 依据: 每步对照错误类别/阶段/钩子触发序列/副作用回滚标记",
			seed, sc.stages, len(sc.edges), sc.terminals, len(sc.hooks), len(sc.requests),
			nm.History())
	}
}

func naiveKindOf(err error) int {
	var k int
	_, scanErr := fmt.Sscanf(err.Error(), "naive: %d", &k)
	if scanErr != nil {
		// 错误消息格式为 "naive: <kind 名>"，按前缀判定。
		msg := err.Error()
		switch {
		case contains(msg, "invalid_argument"):
			return 0
		case contains(msg, "terminal_stage"):
			return 1
		case contains(msg, "transition_not_allowed"):
			return 2
		default:
			return 3
		}
	}
	return k
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func toStages(ss []string) []ontology.Stage {
	out := make([]ontology.Stage, len(ss))
	for i, s := range ss {
		out[i] = ontology.Stage(s)
	}
	return out
}

func toOntologyEdges(edges []naive.Edge) []ontology.Edge {
	out := make([]ontology.Edge, len(edges))
	for i, e := range edges {
		out[i] = ontology.Edge{From: ontology.Stage(e.From), To: ontology.Stage(e.To)}
	}
	return out
}

func traceRecEqual(f ontology.TransitionRecord, n naive.Record) bool {
	if string(f.From) != n.From || string(f.To) != n.To || f.OK != n.OK {
		return false
	}
	if !f.OK && int(f.Kind) != int(n.Kind) {
		return false
	}
	if len(f.Fired) != len(n.Fired) {
		return false
	}
	for i := range f.Fired {
		if f.Fired[i].Hook != n.Fired[i].Hook ||
			int(f.Fired[i].Kind) != int(n.Fired[i].Kind) ||
			f.Fired[i].Failed != n.Fired[i].Failed {
			return false
		}
	}
	return true
}

func stagesEqual(f []ontology.Stage, n []naive.Stage) bool {
	if len(f) != len(n) {
		return false
	}
	for i := range f {
		if string(f[i]) != n[i] {
			return false
		}
	}
	return true
}

func sideEffectsEqual(f []ontology.SideEffect, n []naive.SideEffect) bool {
	if len(f) != len(n) {
		return false
	}
	for i := range f {
		if f[i].Hook != n[i].Hook || f[i].Detail != n[i].Detail || f[i].RolledBack != n[i].RolledBack {
			return false
		}
	}
	return true
}
