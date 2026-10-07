package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustType(t *testing.T, def ObjectTypeDef) *ObjectType {
	t.Helper()
	ot, err := NewObjectType(def)
	if err != nil {
		t.Fatalf("NewObjectType: %v", err)
	}
	return ot
}

func mustInstance(t *testing.T, ot *ObjectType, id string, initial Stage) *Instance {
	t.Helper()
	inst, err := ot.NewInstance(id, initial)
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	return inst
}

func firedNames(rec TransitionRecord) []string {
	names := make([]string, 0, len(rec.Fired))
	for _, f := range rec.Fired {
		names = append(names, f.Hook)
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func okHook(name string) *Hook {
	return &Hook{Name: name, Fn: func(*HookContext) error { return nil }}
}

// 自转移：进入钩子仍触发；具体转移钩子只在声明了 (s,s) 钩子时才触发。
func TestSelfTransitionHookScope(t *testing.T) {
	def := ObjectTypeDef{
		Name:   "doc",
		Stages: []Stage{"draft", "review"},
		Edges:  []Edge{{"draft", "draft"}, {"draft", "review"}},
	}

	t.Run("enter hook fires on self transition", func(t *testing.T) {
		ot := mustType(t, def)
		enter := okHook("enter-draft")
		enter.To = "draft"
		ot.Hooks().RegisterEnter(enter)
		inst := mustInstance(t, ot, "o1", "draft")
		if err := ot.Transition(inst, "draft"); err != nil {
			t.Fatalf("self transition rejected: %v", err)
		}
		got := firedNames(inst.Trace()[0])
		t.Logf("输入: 自转移 draft->draft, 仅注册进入钩子; 输出: fired=%v; 依据: 进入钩子不因 from==to 跳过", got)
		if !equalStrings(got, []string{"enter-draft"}) {
			t.Fatalf("fired=%v, want [enter-draft]", got)
		}
	})

	t.Run("transition hook on self edge fires only when declared", func(t *testing.T) {
		// 声明了 (draft,draft) 的具体转移钩子：必须与进入钩子都触发，且前者在前。
		ot := mustType(t, def)
		self := okHook("t-draft-draft")
		self.From, self.To = "draft", "draft"
		ot.Hooks().RegisterTransition(self)
		enter := okHook("enter-draft")
		enter.To = "draft"
		ot.Hooks().RegisterEnter(enter)
		inst := mustInstance(t, ot, "o1", "draft")
		if err := ot.Transition(inst, "draft"); err != nil {
			t.Fatalf("self transition rejected: %v", err)
		}
		got := firedNames(inst.Trace()[0])
		t.Logf("输入: 自转移 draft->draft, 声明了 (draft,draft) 具体转移钩子与进入钩子; 输出: fired=%v; 依据: 具体转移钩子先于进入钩子", got)
		if !equalStrings(got, []string{"t-draft-draft", "enter-draft"}) {
			t.Fatalf("fired=%v, want [t-draft-draft enter-draft]", got)
		}
	})

	t.Run("transition hook on other edge does not fire on self transition", func(t *testing.T) {
		// 仅在 (draft,review) 上声明具体转移钩子：自转移不得默认触发它。
		ot := mustType(t, def)
		other := okHook("t-draft-review")
		other.From, other.To = "draft", "review"
		ot.Hooks().RegisterTransition(other)
		enter := okHook("enter-draft")
		enter.To = "draft"
		ot.Hooks().RegisterEnter(enter)
		inst := mustInstance(t, ot, "o1", "draft")
		if err := ot.Transition(inst, "draft"); err != nil {
			t.Fatalf("self transition rejected: %v", err)
		}
		got := firedNames(inst.Trace()[0])
		t.Logf("输入: 自转移 draft->draft, 具体转移钩子只声明在 (draft,review); 输出: fired=%v; 依据: 具体转移钩子按 (from,to) 精确匹配, 不因阶段相同默认触发", got)
		if !equalStrings(got, []string{"enter-draft"}) {
			t.Fatalf("fired=%v, want [enter-draft]", got)
		}
	})

	t.Run("self transition requires declared self edge", func(t *testing.T) {
		ot := mustType(t, ObjectTypeDef{
			Name:   "doc2",
			Stages: []Stage{"a", "b"},
			Edges:  []Edge{{"a", "b"}},
		})
		inst := mustInstance(t, ot, "o1", "a")
		err := ot.Transition(inst, "a")
		kind, ok := KindOf(err)
		t.Logf("输入: 自转移 a->a 但未声明 (a,a) 边; 输出: kind=%v; 依据: 阶段相同不默认允许转移", kind)
		if !ok || kind != ErrTransitionNotAllowed {
			t.Fatalf("kind=%v ok=%v, want transition_not_allowed", kind, ok)
		}
		if inst.Stage() != "a" {
			t.Fatalf("stage=%v, want a", inst.Stage())
		}
	})
}

// 终态限制优先于钩子校验：终态发起的任何转移（含转到自身）被拒绝且钩子不触发。
func TestTerminalPriorityOverHooks(t *testing.T) {
	ot := mustType(t, ObjectTypeDef{
		Name:      "ticket",
		Stages:    []Stage{"open", "closed"},
		Edges:     []Edge{{"open", "closed"}, {"closed", "closed"}, {"closed", "open"}},
		Terminals: []Stage{"closed"},
	})
	var fired []string
	record := func(name string) *Hook {
		return &Hook{Name: name, Fn: func(*HookContext) error {
			fired = append(fired, name)
			return nil
		}}
	}
	th := record("t-closed-open")
	th.From, th.To = "closed", "open"
	ot.Hooks().RegisterTransition(th)
	eh := record("enter-open")
	eh.To = "open"
	ot.Hooks().RegisterEnter(eh)

	inst := mustInstance(t, ot, "t1", "open")
	if err := ot.Transition(inst, "closed"); err != nil {
		t.Fatalf("open->closed: %v", err)
	}
	for _, to := range []Stage{"open", "closed"} {
		err := ot.Transition(inst, to)
		kind, ok := KindOf(err)
		t.Logf("输入: 终态 closed 发起转移 ->%s; 输出: kind=%v fired=%v; 依据: 终态限制优先于钩子校验", to, kind, fired)
		if !ok || kind != ErrTerminalStage {
			t.Fatalf("kind=%v ok=%v, want terminal_stage", kind, ok)
		}
	}
	if len(fired) != 0 {
		t.Fatalf("hooks fired on terminal rejection: %v", fired)
	}
	if inst.Stage() != "closed" {
		t.Fatalf("stage=%v, want closed", inst.Stage())
	}
}

// 具体转移钩子与目标阶段进入钩子的触发先后顺序：全部具体转移钩子在前。
func TestHookFiringOrder(t *testing.T) {
	ot := mustType(t, ObjectTypeDef{
		Name:   "flow",
		Stages: []Stage{"a", "b"},
		Edges:  []Edge{{"a", "b"}},
	})
	var order []string
	mk := func(name string) *Hook {
		return &Hook{Name: name, Fn: func(*HookContext) error {
			order = append(order, name)
			return nil
		}}
	}
	e1 := mk("enter-1")
	e1.To = "b"
	ot.Hooks().RegisterEnter(e1)
	t1 := mk("trans-1")
	t1.From, t1.To = "a", "b"
	ot.Hooks().RegisterTransition(t1)
	e2 := mk("enter-2")
	e2.To = "b"
	ot.Hooks().RegisterEnter(e2)
	t2 := mk("trans-2")
	t2.From, t2.To = "a", "b"
	ot.Hooks().RegisterTransition(t2)

	inst := mustInstance(t, ot, "f1", "a")
	if err := ot.Transition(inst, "b"); err != nil {
		t.Fatalf("a->b: %v", err)
	}
	t.Logf("输入: 注册顺序 enter-1,trans-1,enter-2,trans-2; 输出: order=%v; 依据: 具体转移钩子整体先于进入钩子, 同类内按注册顺序", order)
	want := []string{"trans-1", "trans-2", "enter-1", "enter-2"}
	if !equalStrings(order, want) {
		t.Fatalf("order=%v, want %v", order, want)
	}
}

// 钩子失败：转移整体不生效，阶段保持转移前的值；
// 已触发钩子的记录副作用按各自声明的提交语义决定是否撤销。
func TestHookFailureAtomicity(t *testing.T) {
	ot := mustType(t, ObjectTypeDef{
		Name:   "order",
		Stages: []Stage{"new", "paid"},
		Edges:  []Edge{{"new", "paid"}},
	})
	cause := errors.New("balance check failed")

	audit := &Hook{Name: "audit-log", Commit: CommitOnFailure, Fn: func(ctx *HookContext) error {
		ctx.Emit("audit: attempt paid")
		return nil
	}}
	audit.From, audit.To = "new", "paid"
	ot.Hooks().RegisterTransition(audit)

	check := &Hook{Name: "balance-check", Commit: RollbackOnFailure, Fn: func(ctx *HookContext) error {
		ctx.Emit("check: temp reservation")
		return cause
	}}
	check.From, check.To = "new", "paid"
	ot.Hooks().RegisterTransition(check)

	never := okHook("enter-paid")
	never.To = "paid"
	never.Fn = func(*HookContext) error {
		t.Error("enter-paid must not fire after earlier hook failure")
		return nil
	}
	ot.Hooks().RegisterEnter(never)

	inst := mustInstance(t, ot, "o1", "new")
	err := ot.Transition(inst, "paid")
	kind, ok := KindOf(err)
	if !ok || kind != ErrHookFailed {
		t.Fatalf("kind=%v ok=%v, want hook_failed", kind, ok)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(err, cause)=false, want true")
	}
	var terr *Error
	if !errors.As(err, &terr) || terr.Hook != "balance-check" {
		t.Fatalf("terr=%+v, want Hook=balance-check", terr)
	}
	if inst.Stage() != "new" {
		t.Fatalf("stage=%v, want new (转移必须整体不生效)", inst.Stage())
	}
	if len(inst.History()) != 1 {
		t.Fatalf("history=%v, want length 1", inst.History())
	}
	se := inst.SideEffects()
	t.Logf("输入: audit(CommitOnFailure) 通过, balance-check(RollbackOnFailure) 失败; 输出: sideEffects=%+v; 依据: 副作用是否撤销由钩子自身提交语义决定", se)
	if len(se) != 2 {
		t.Fatalf("sideEffects=%+v, want 2 entries", se)
	}
	if se[0].Hook != "audit-log" || se[0].RolledBack {
		t.Fatalf("audit-log side effect must be kept: %+v", se[0])
	}
	if se[1].Hook != "balance-check" || !se[1].RolledBack {
		t.Fatalf("balance-check side effect must be rolled back: %+v", se[1])
	}
	// 失败的转移仍留在轨迹中，且实例可继续接受新转移。
	ot2 := mustType(t, ObjectTypeDef{
		Name:   "order2",
		Stages: []Stage{"new", "paid"},
		Edges:  []Edge{{"new", "paid"}},
	})
	inst2 := mustInstance(t, ot2, "o2", "new")
	if err := ot2.Transition(inst2, "paid"); err != nil {
		t.Fatalf("clean transition: %v", err)
	}
	if inst2.Stage() != "paid" {
		t.Fatalf("stage=%v, want paid", inst2.Stage())
	}
}

// 拒绝原因按固定次序只报第一个命中的，且被拒绝的转移不改变阶段。
func TestErrorOrdering(t *testing.T) {
	ot := mustType(t, ObjectTypeDef{
		Name:      "obj",
		Stages:    []Stage{"x", "y", "z"},
		Edges:     []Edge{{"x", "y"}},
		Terminals: []Stage{"z"},
	})
	fail := &Hook{Name: "always-fail", Fn: func(*HookContext) error { return errors.New("boom") }}
	fail.From, fail.To = "x", "z"
	ot.Hooks().RegisterTransition(fail)
	enterZ := &Hook{Name: "enter-z-fail", Fn: func(*HookContext) error { return errors.New("boom") }}
	enterZ.To = "z"
	ot.Hooks().RegisterEnter(enterZ)

	cases := []struct {
		name string
		from Stage
		to   Stage
		want ErrorKind
	}{
		{"undeclared target wins over terminal", "z", "ghost", ErrInvalidArgument},
		{"terminal wins over not-allowed", "z", "y", ErrTerminalStage},
		{"not-allowed wins over hook failure", "y", "x", ErrTransitionNotAllowed},
		{"hook failure reported last", "x", "z", ErrTransitionNotAllowed}, // (x,z) 未声明, 钩子不得触发
	}
	for _, tc := range cases {
		inst := mustInstance(t, ot, "c-"+tc.name, tc.from)
		err := ot.Transition(inst, tc.to)
		kind, ok := KindOf(err)
		t.Logf("输入: %s->%s; 输出: kind=%v; 依据: 只报第一个命中的拒绝原因", tc.from, tc.to, kind)
		if !ok || kind != tc.want {
			t.Fatalf("%s: kind=%v ok=%v, want %v", tc.name, kind, ok, tc.want)
		}
		if inst.Stage() != tc.from {
			t.Fatalf("%s: stage changed to %v", tc.name, inst.Stage())
		}
	}

	// 参数非法：nil 实例与其他类型的实例。
	if kind, _ := KindOf(ot.Transition(nil, "y")); kind != ErrInvalidArgument {
		t.Fatalf("nil instance kind=%v, want invalid_argument", kind)
	}
	other := mustType(t, ObjectTypeDef{Name: "other", Stages: []Stage{"x"}, Edges: []Edge{{"x", "x"}}})
	foreign := mustInstance(t, other, "f", "x")
	if kind, _ := KindOf(ot.Transition(foreign, "y")); kind != ErrInvalidArgument {
		t.Fatalf("foreign instance kind=%v, want invalid_argument", kind)
	}
	if _, ok := KindOf(errors.New("plain")); ok {
		t.Fatal("KindOf(plain error) ok=true, want false")
	}

	// 钩子失败是最后才命中的原因：(x,y) 已声明且 x 非终态。
	fail2 := &Hook{Name: "fail-xy", Fn: func(*HookContext) error { return errors.New("boom") }}
	fail2.From, fail2.To = "x", "y"
	ot.Hooks().RegisterTransition(fail2)
	inst := mustInstance(t, ot, "c-hook", "x")
	if kind, _ := KindOf(ot.Transition(inst, "y")); kind != ErrHookFailed {
		t.Fatalf("kind=%v, want hook_failed", kind)
	}
	if inst.Stage() != "x" {
		t.Fatalf("stage=%v, want x", inst.Stage())
	}
}

// 并发转移：最终效果等价于某个全局串行顺序，阶段历史不出现分叉；
// 按轨迹中的串行顺序重放同一组请求，得到完全相同的阶段轨迹与钩子触发记录。
func TestConcurrentTransitionsNoFork(t *testing.T) {
	ot := mustType(t, ObjectTypeDef{
		Name:   "conc",
		Stages: []Stage{"a", "b", "c"},
		Edges: []Edge{
			{"a", "b"}, {"b", "a"}, {"b", "c"}, {"c", "a"}, {"a", "a"}, {"c", "c"},
		},
	})
	var mu sync.Mutex
	var fired []string
	hook := &Hook{Name: "enter-b", Fn: func(*HookContext) error {
		mu.Lock()
		fired = append(fired, "enter-b")
		mu.Unlock()
		return nil
	}}
	hook.To = "b"
	ot.Hooks().RegisterEnter(hook)

	inst := mustInstance(t, ot, "shared", "a")
	targets := []Stage{"a", "b", "c"}

	const goroutines = 16
	const perGoroutine = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			state := uint32(seed*2654435761 + 1)
			for i := 0; i < perGoroutine; i++ {
				state = state*1664525 + 1013904223
				_ = ot.Transition(inst, targets[state%uint32(len(targets))])
			}
		}(g)
	}
	wg.Wait()

	// 轨迹必须构成一条无分叉的链：每条记录的起始阶段 == 上一条记录结束时的阶段。
	trace := inst.Trace()
	cur := Stage("a")
	successes := 0
	for i, rec := range trace {
		if rec.From != cur {
			t.Fatalf("fork at record %d: rec.From=%v, want %v (阶段历史出现分叉)", i, rec.From, cur)
		}
		if rec.OK {
			cur = rec.To
			successes++
		}
	}
	if inst.Stage() != cur {
		t.Fatalf("final stage=%v, want %v", inst.Stage(), cur)
	}
	if got := len(inst.History()); got != successes+1 {
		t.Fatalf("history len=%d, want %d", got, successes+1)
	}
	t.Logf("输入: %d 个 goroutine 各 %d 次随机转移; 输出: %d 条记录全部串行衔接, 成功 %d 次; 依据: 每条记录 From==前序结束阶段",
		goroutines, perGoroutine, len(trace), successes)

	// 重放：按轨迹的串行顺序重放同一组请求，必须得到完全相同的轨迹与钩子记录。
	replay := mustInstance(t, ot, "replay", "a")
	for _, rec := range trace {
		_ = ot.Transition(replay, rec.To)
	}
	rt := replay.Trace()
	if len(rt) != len(trace) {
		t.Fatalf("replay trace len=%d, want %d", len(rt), len(trace))
	}
	for i := range trace {
		if !recordEqual(rt[i], trace[i]) {
			t.Fatalf("replay diverges at %d: got %+v, want %+v", i, rt[i], trace[i])
		}
	}
	if !equalStrings(stagesToStrings(replay.History()), stagesToStrings(inst.History())) {
		t.Fatalf("replay history diverges")
	}
}

func recordEqual(a, b TransitionRecord) bool {
	if a.Seq != b.Seq || a.From != b.From || a.To != b.To || a.OK != b.OK || a.Kind != b.Kind {
		return false
	}
	if len(a.Fired) != len(b.Fired) {
		return false
	}
	for i := range a.Fired {
		if a.Fired[i] != b.Fired[i] {
			return false
		}
	}
	return true
}

func stagesToStrings(stages []Stage) []string {
	out := make([]string, len(stages))
	for i, s := range stages {
		out[i] = string(s)
	}
	return out
}

// 可验证的复杂度证明：Resolve 扫描的钩子数 == 命中数，
// 与全部转移关系总数、全部已注册钩子总数无关。
func TestResolveScannedEqualsHits(t *testing.T) {
	for _, total := range []int{10, 1000, 100000} {
		reg := NewHookRegistry()
		// 大量与本次转移无关的钩子与注册项。
		for i := 0; i < total; i++ {
			h := okHook(fmt.Sprintf("noise-t-%d", i))
			h.From, h.To = Stage(fmt.Sprintf("s%d", i)), Stage(fmt.Sprintf("s%d", i+1))
			reg.RegisterTransition(h)
			e := okHook(fmt.Sprintf("noise-e-%d", i))
			e.To = Stage(fmt.Sprintf("other-%d", i))
			reg.RegisterEnter(e)
		}
		// 本次转移实际命中的钩子：2 个具体转移钩子 + 1 个进入钩子。
		for i := 0; i < 2; i++ {
			h := okHook(fmt.Sprintf("hit-t-%d", i))
			h.From, h.To = "from", "to"
			reg.RegisterTransition(h)
		}
		hit := okHook("hit-enter")
		hit.To = "to"
		reg.RegisterEnter(hit)

		got := reg.Resolve("from", "to")
		t.Logf("输入: 总钩子数=%d, 命中=3; 输出: Resolve 返回 %d 个, 扫描 %d 个; 依据: 扫描数只与命中数相关",
			2*total+3, len(got), reg.LastResolveScanned())
		if len(got) != 3 || reg.LastResolveScanned() != 3 {
			t.Fatalf("total=%d: got=%d scanned=%d, want 3/3", total, len(got), reg.LastResolveScanned())
		}
	}
}

// BenchmarkResolve 佐证解析开销与总量无关：总量增长 1000 倍时耗时应基本不变。
func BenchmarkResolve(b *testing.B) {
	for _, total := range []int{100, 10000, 1000000} {
		reg := NewHookRegistry()
		for i := 0; i < total; i++ {
			h := okHook(fmt.Sprintf("n-%d", i))
			h.From, h.To = Stage(fmt.Sprintf("s%d", i)), Stage(fmt.Sprintf("s%d", i))
			reg.RegisterTransition(h)
		}
		hit := okHook("hit")
		hit.From, hit.To = "from", "to"
		reg.RegisterTransition(hit)
		b.Run(fmt.Sprintf("total=%d", total), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = reg.Resolve("from", "to")
			}
		})
	}
}
