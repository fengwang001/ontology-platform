package ontology

import (
	"fmt"
	"testing"
)

// 直接自我触发：同一 (动作, 参数) 组合在触发前被拒绝，
// 拒绝发生在内层前置条件求值之前，且不改变已计算的写入计划。
func TestSelfTriggerDirectRejectedBeforePreconditions(t *testing.T) {
	store := NewStore()
	preEvals := 0

	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name: "loop",
		Preconditions: []Condition{{
			Name:  "counting",
			Check: func(v *View) error { preEvals++; return nil },
		}},
		Calls: []CallSpec{{Action: "loop", Critical: false}},
		Run: func(ctx *Context) error {
			ctx.Put(Object{ID: "before", Type: "marker", Props: map[string]any{}})
			res := ctx.Invoke("loop", Args{"x": 1}, false)
			if res.Outcome != OutcomeSelfTriggerRejected {
				return fmt.Errorf("expected self-trigger rejection, got %v", res.Outcome)
			}
			ctx.Put(Object{ID: "after", Type: "marker", Props: map[string]any{}})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	eng := mustEngine(t, reg, store)
	res := eng.Execute("loop", Args{"x": 1})

	if !res.Committed {
		t.Fatalf("rejection must not abort the chain: %+v", res)
	}
	if preEvals != 1 {
		t.Fatalf("guard must run before inner precondition evaluation, preEvals=%d", preEvals)
	}
	for _, id := range []string{"before", "after"} {
		if _, ok := store.Get(id); !ok {
			t.Fatalf("rejection must not discard computed write plan, missing %q", id)
		}
	}
	events := findEvents(res.Trace, OutcomeSelfTriggerRejected)
	if len(events) != 1 || events[0].Phase != PhaseGuard {
		t.Fatalf("expected one guard-phase rejection event, got %+v", events)
	}
}

// 间接自我触发：A -> B -> C -> A（与 A 相同的参数组合）在深度 3 处被拒绝；
// 同时验证“同参数不同动作”不构成自我触发（A->B->C 均被放行）。
func TestSelfTriggerIndirectAtDepth(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	shared := Args{"x": 1}

	mk := func(name, next string, critical bool) *Action {
		return &Action{
			Name:  name,
			Calls: []CallSpec{{Action: next, Critical: critical}},
			Run: func(ctx *Context) error {
				ctx.Put(Object{ID: "wrote-" + name, Type: "marker", Props: map[string]any{}})
				ctx.Invoke(next, shared, critical)
				return nil
			},
		}
	}
	// C 对 A 的回调声明为关键调用：拒绝仍然只记录、不放弃链条。
	for _, a := range []*Action{mk("A", "B", true), mk("B", "C", true), mk("C", "A", true)} {
		if err := reg.Register(a); err != nil {
			t.Fatal(err)
		}
	}

	eng := mustEngine(t, reg, store)
	res := eng.Execute("A", shared)

	if !res.Committed {
		t.Fatalf("rejection must not abort even for a critical call: %+v", res)
	}
	events := findEvents(res.Trace, OutcomeSelfTriggerRejected)
	if len(events) != 1 {
		t.Fatalf("expected exactly one rejection, got %+v", events)
	}
	if events[0].Path != "A/B/C/A" || events[0].Depth != 3 {
		t.Fatalf("rejection must happen at depth 3 on path A/B/C/A, got %+v", events[0])
	}
	for _, id := range []string{"wrote-A", "wrote-B", "wrote-C"} {
		if _, ok := store.Get(id); !ok {
			t.Fatalf("writes computed before the rejection must be kept, missing %q", id)
		}
	}
}

// 放行边界：同一动作携带不同参数不触发拒绝，且嵌套深度没有预设上限。
func TestSelfTriggerBoundaryDifferentArgsAllowed(t *testing.T) {
	store := NewStore()
	const depth = 64

	reg := NewRegistry()
	if err := reg.Register(&Action{
		Name:  "countdown",
		Calls: []CallSpec{{Action: "countdown", Critical: true}},
		Run: func(ctx *Context) error {
			n := ctx.Arg("n").(int)
			ctx.Put(Object{ID: fmt.Sprintf("node-%d", n), Type: "marker", Props: map[string]any{}})
			if n > 0 {
				res := ctx.Invoke("countdown", Args{"n": n - 1}, true)
				if !res.OK() {
					return fmt.Errorf("distinct args must not be rejected: %v", res.Outcome)
				}
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	eng := mustEngine(t, reg, store)
	res := eng.Execute("countdown", Args{"n": depth})
	if !res.Committed {
		t.Fatalf("depth %d with distinct args must commit: %+v", depth, res)
	}
	if len(findEvents(res.Trace, OutcomeSelfTriggerRejected)) != 0 {
		t.Fatal("no rejection expected for distinct (action, args) combinations")
	}
	for n := 0; n <= depth; n++ {
		if _, ok := store.Get(fmt.Sprintf("node-%d", n)); !ok {
			t.Fatalf("missing node-%d: full depth must execute", n)
		}
	}
}

// 守卫开销验证：每次嵌套调用恰好一次查表，总次数等于嵌套调用次数，
// 与当前链条深度成正比，与系统历史执行过的链条总量无关。
func TestGuardCostBoundedByChainDepth(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()

	const depth = 32
	// d0 -> d1 -> ... -> d31，共 31 次嵌套调用。
	for i := 0; i < depth; i++ {
		name := fmt.Sprintf("d%d", i)
		a := &Action{Name: name, Run: func(ctx *Context) error { return nil }}
		if i+1 < depth {
			next := fmt.Sprintf("d%d", i+1)
			a.Calls = []CallSpec{{Action: next, Critical: true}}
			a.Run = func(ctx *Context) error {
				ctx.Invoke(next, Args{"i": i}, true)
				return nil
			}
		}
		if err := reg.Register(a); err != nil {
			t.Fatal(err)
		}
	}
	// 一个无关的独立动作，用于堆积历史执行总量。
	if err := reg.Register(&Action{
		Name: "noop",
		Run:  func(ctx *Context) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}

	eng := mustEngine(t, reg, store)

	first := eng.Execute("d0", Args{})
	if !first.Committed {
		t.Fatalf("chain must commit: %+v", first)
	}
	if got := first.Trace.GuardChecks(); got != depth-1 {
		t.Fatalf("guard checks must equal nested invoke count %d, got %d", depth-1, got)
	}

	// 堆积历史：执行大量无关链条后，守卫开销不得增长。
	for i := 0; i < 2000; i++ {
		eng.Execute("noop", Args{"i": i})
	}
	second := eng.Execute("d0", Args{})
	if got := second.Trace.GuardChecks(); got != depth-1 {
		t.Fatalf("guard checks must not depend on chain history, got %d want %d", got, depth-1)
	}
}
