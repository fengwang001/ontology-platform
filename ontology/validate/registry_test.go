package validate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// target 是测试使用的通用校验对象；钩子行为由外部脚本表驱动，
// 这样朴素串行模型与真实实现可以共享同一套钩子语义。
type target struct {
	value int
}

// scriptHook 按 (hookID -> 对本次 target 的结论) 的脚本运行；
// hookCalls 记录实际进入钩子函数的次序，用于核对“执行/未执行”的可观察性。
type scriptHook struct {
	id     string
	group  string
	script func(t target) (Outcome, error)
	calls  *[]string
	panics bool
}

func (h scriptHook) toHook() Hook[target] {
	calls := h.calls
	return Hook[target]{
		ID:    h.id,
		Group: h.group,
		Fn: func(_ context.Context, t target) (Outcome, error) {
			*calls = append(*calls, h.id)
			if h.panics {
				panic("boom:" + h.id)
			}
			return h.script(t)
		},
	}
}

func approveHook(id, group string, calls *[]string) Hook[target] {
	return scriptHook{id: id, group: group, calls: calls,
		script: func(target) (Outcome, error) { return Outcome{Decision: Approve}, nil }}.toHook()
}

func rejectHook(id, group, reason string, calls *[]string) Hook[target] {
	return scriptHook{id: id, group: group, calls: calls,
		script: func(target) (Outcome, error) { return Outcome{Decision: Reject, Reason: reason}, nil }}.toHook()
}

func errorHook(id, group string, calls *[]string) Hook[target] {
	return scriptHook{id: id, group: group, calls: calls,
		script: func(target) (Outcome, error) { return Outcome{}, errors.New("hook failure:" + id) }}.toHook()
}

func panicHook(id, group string, calls *[]string) Hook[target] {
	h := scriptHook{id: id, group: group, calls: calls, panics: true,
		script: func(target) (Outcome, error) { return Outcome{Decision: Approve}, nil }}
	return h.toHook()
}

func mustRegistry(t testing.TB, groups ...GroupSpec) *Registry[target] {
	t.Helper()
	r, err := NewRegistry[target](groups...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func TestGroupOrdering(t *testing.T) {
	r := mustRegistry(t,
		GroupSpec{Name: "low", Priority: 1, ShortCircuit: true},
		GroupSpec{Name: "high", Priority: 100, ShortCircuit: true},
		GroupSpec{Name: "mid", Priority: 50, ShortCircuit: true},
	)
	snap := r.Snapshot()
	got := groupNames(snap)
	want := []string{"high", "mid", "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group order = %v, want %v", got, want)
	}
}

func TestRegisterOrderWithinGroup(t *testing.T) {
	var calls []string
	r := mustRegistry(t, GroupSpec{Name: "g", Priority: 1, ShortCircuit: false})
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := r.Register(approveHook(id, "g", &calls)); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
	rep, err := r.Validate(context.Background(), target{})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"a", "b", "c", "d"}) {
		t.Fatalf("execution order = %v, want registration order", calls)
	}
	if rep.FinalDecision != Approve {
		t.Fatalf("decision = %v, want Approve", rep.FinalDecision)
	}
}

func TestDuplicateAndUndeclaredGroup(t *testing.T) {
	var calls []string
	r := mustRegistry(t, GroupSpec{Name: "g", Priority: 1, ShortCircuit: true})
	if err := r.Register(approveHook("h1", "g", &calls)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(approveHook("h1", "g", &calls)); err == nil {
		t.Fatal("duplicate id must error")
	}
	if err := r.Register(approveHook("h2", "nope", &calls)); err == nil {
		t.Fatal("undeclared group must error")
	}
	if err := (Hook[target]{ID: "h3"}).validate(); err == nil {
		t.Fatal("empty group must error")
	}
	if len(r.Snapshot().HookIDs()) != 1 {
		t.Fatal("failed registrations must not mutate registry")
	}
}

func TestBatchAtomicityAndOrder(t *testing.T) {
	var calls []string
	r := mustRegistry(t, GroupSpec{Name: "g", Priority: 1, ShortCircuit: false})
	// 批次内含一个引用未声明分组的非法条目：整体必须不生效。
	err := r.RegisterBatch(
		Registration[target]{Hook: approveHook("a", "g", &calls)},
		Registration[target]{Hook: approveHook("b", "nope", &calls)},
	)
	if err == nil {
		t.Fatal("batch with invalid entry must fail")
	}
	if len(r.Snapshot().HookIDs()) != 0 {
		t.Fatal("failed batch must be fully atomic, no partial registration")
	}

	// 交错声明顺序：a(g2), a1(g1), b(g2)。批次内相对顺序必须原样保留。
	r2 := mustRegistry(t,
		GroupSpec{Name: "g1", Priority: 100, ShortCircuit: false},
		GroupSpec{Name: "g2", Priority: 1, ShortCircuit: false},
	)
	err = r2.RegisterBatch(
		Registration[target]{Hook: approveHook("a", "g2", &calls)},
		Registration[target]{Hook: approveHook("a1", "g1", &calls)},
		Registration[target]{Hook: approveHook("b", "g2", &calls)},
	)
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	snap := r2.Snapshot()
	g1 := snap.Groups[0]
	g2 := snap.Groups[1]
	if g1.Name != "g1" || g2.Name != "g2" {
		t.Fatalf("group order mismatch: %s before %s", g1.Name, g2.Name)
	}
	if got := hookIDs(g2); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("g2 intra-batch order = %v, want [a b] by declared relative order", got)
	}
	if _, err2 := r2.Validate(context.Background(), target{}); err2 != nil {
		t.Fatal(err2)
	}
	if !reflect.DeepEqual(calls, []string{"a1", "a", "b"}) {
		t.Fatalf("execution = %v, want a1 then a,b (batch relative order preserved)", calls)
	}
}

func TestUnregisterReregisterAppendsToEnd(t *testing.T) {
	var calls []string
	r := mustRegistry(t, GroupSpec{Name: "g", Priority: 1, ShortCircuit: false})
	for _, id := range []string{"a", "b", "c"} {
		if err := r.Register(approveHook(id, "g", &calls)); err != nil {
			t.Fatal(err)
		}
	}
	if !r.Unregister("b") {
		t.Fatal("b should exist")
	}
	if r.Unregister("b") {
		t.Fatal("second unregister of b must report not-existed")
	}
	// 重新注册同 ID 必须视为新注册，排到当前末尾（a, c 之后）。
	if err := r.Register(approveHook("b", "g", &calls)); err != nil {
		t.Fatal(err)
	}
	if got := r.Snapshot().HookIDs(); !reflect.DeepEqual(got, []string{"a", "c", "b"}) {
		t.Fatalf("after unregister/reregister order = %v, want [a c b]", got)
	}
}

func TestSnapshotIsolationDuringCall(t *testing.T) {
	var calls []string
	r := mustRegistry(t,
		GroupSpec{Name: "g1", Priority: 100, ShortCircuit: false},
		GroupSpec{Name: "g2", Priority: 1, ShortCircuit: false},
	)
	for _, id := range []string{"a", "b"} {
		_ = r.Register(approveHook(id, "g1", &calls))
	}
	release := make(chan struct{})
	var inside atomic.Bool
	// g1 的第一个钩子在执行途中触发注销/注册，随后等待放行，
	// 验证本次调用从头到尾使用同一份快照。
	_ = r.Register(Hook[target]{
		ID: "mutator", Group: "g1",
		Fn: func(_ context.Context, _ target) (Outcome, error) {
			calls = append(calls, "mutator")
			r.Unregister("b")
			_ = r.Register(approveHook("c", "g2", &calls))
			inside.Store(true)
			<-release
			return Outcome{Decision: Approve}, nil
		},
	})
	// 重新排列：mutator 最后注册，执行顺序 a,b,mutator。
	done := make(chan *Report[target])
	go func() {
		rep, err := r.Validate(context.Background(), target{})
		if err != nil {
			t.Errorf("validate: %v", err)
		}
		done <- rep
	}()
	// 等待进入 mutator（此时注销/注册已发生）。
	for !inside.Load() {
	}
	// mutator 仍在执行期间：本次调用的快照必须仍是旧集合 a,b,mutator（无 c，b 未消失）。
	release <- struct{}{}
	rep := <-done
	want := []string{"a", "b", "mutator"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("in-flight call observed mutation: calls=%v want %v", calls, want)
	}
	if ids := rep.Snapshot.HookIDs(); !reflect.DeepEqual(ids, want) {
		t.Fatalf("report snapshot = %v, want %v", ids, want)
	}
	// 调用结束后，新调用看到新快照。
	if ids := r.Snapshot().HookIDs(); !reflect.DeepEqual(ids, []string{"a", "mutator", "c"}) {
		t.Fatalf("post-call snapshot = %v, want [a mutator c]", ids)
	}
}

func TestDeclareGroupAtRuntime(t *testing.T) {
	var calls []string
	r := mustRegistry(t, GroupSpec{Name: "g", Priority: 1, ShortCircuit: true})
	if err := r.DeclareGroup(GroupSpec{Name: "higher", Priority: 99, ShortCircuit: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.DeclareGroup(GroupSpec{Name: "higher", Priority: 1}); err == nil {
		t.Fatal("duplicate runtime group must error")
	}
	if err := r.Register(approveHook("h", "higher", &calls)); err != nil {
		t.Fatal(err)
	}
	if got := groupNames(r.Snapshot()); !reflect.DeepEqual(got, []string{"higher", "g"}) {
		t.Fatalf("runtime group ordering = %v", got)
	}
}

func TestVersionMonotonic(t *testing.T) {
	var calls []string
	r := mustRegistry(t, GroupSpec{Name: "g", Priority: 1, ShortCircuit: true})
	v0 := r.Snapshot().Version
	_ = r.Register(approveHook("a", "g", &calls))
	_ = r.RegisterBatch(Registration[target]{Hook: approveHook("b", "g", &calls)})
	r.Unregister("a")
	if r.Unregister("ghost") {
		t.Fatal("unregister missing id should not bump version")
	}
	vEnd := r.Snapshot()
	if !(vEnd.Version > v0) {
		t.Fatalf("version must increase on successful mutations: %d -> %d", v0, vEnd.Version)
	}
	if vEnd.Version != v0+3 {
		t.Fatalf("version = %d, want %d (3 successful mutations only)", vEnd.Version, v0+3)
	}
}

func TestConcurrentNoDataRace(t *testing.T) {
	r := mustRegistry(t,
		GroupSpec{Name: "g1", Priority: 2, ShortCircuit: true},
		GroupSpec{Name: "g2", Priority: 1, ShortCircuit: false},
	)
	var counter atomic.Int64
	hookFor := func(id, group string, reject bool) Hook[target] {
		return Hook[target]{ID: id, Group: group,
			Fn: func(context.Context, target) (Outcome, error) {
				counter.Add(1)
				if reject {
					return Outcome{Decision: Reject, Reason: "no"}, nil
				}
				return Outcome{Decision: Approve}, nil
			}}
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("h%d", i)
			_ = r.Register(hookFor(id, "g1", i%3 == 0))
			_, _ = r.Validate(context.Background(), target{})
			r.Unregister(id)
			_, _ = r.Validate(context.Background(), target{})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			_ = r.Register(hookFor(fmt.Sprintf("x%d", j), "g2", false))
		}
	}()
	wg.Wait()
}

func groupNames[T any](s Snapshot[T]) []string {
	names := make([]string, len(s.Groups))
	for i, g := range s.Groups {
		names[i] = g.Name
	}
	return names
}

func hookIDs[T any](g SnapshotGroup[T]) []string {
	ids := make([]string, len(g.Hooks))
	for i, h := range g.Hooks {
		ids[i] = h.ID
	}
	return ids
}
